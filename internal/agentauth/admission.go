package agentauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/kado-so/search/internal/agentkey"
	"golang.org/x/crypto/argon2"
)

const (
	admissionStartProofType      = "agent-admission-start+jws"
	admissionManagementProofType = "agent-admission-authorization+jws"
	admissionSessionProofType    = "agent-session-possession+jws"
	admissionStartOperation      = "start-admission"
	admissionCompleteOperation   = "complete-admission"
	admissionBindingDomain       = "kado-agent-admission-binding-v1"
	argonAlgorithm               = "argon2id-deterministic-v1"
	argonVersion                 = 19
	argonTagLength               = 32
	argonCounterMode             = "uint32-be"
)

var argonDomain = []byte("agent-auth-argon2id-pow-v1\n")

type admissionStartPayload struct {
	Audience         string    `json:"audience"`
	ClientNonce      string    `json:"client_nonce"`
	CreateIfMissing  bool      `json:"create_if_missing"`
	ExpiresAt        int64     `json:"exp"`
	IssuedAt         int64     `json:"iat"`
	Issuer           string    `json:"issuer"`
	JTI              string    `json:"jti"`
	Operation        string    `json:"operation"`
	RequestedScopes  []string  `json:"requested_scopes"`
	SessionPublicJWK PublicJWK `json:"session_public_jwk"`
	Version          string    `json:"version"`
}

type admissionChallenge struct {
	Algorithm    string `json:"algorithm"`
	ArgonVersion int    `json:"argon2_version"`
	Salt         string `json:"salt"`
	MemoryKiB    uint32 `json:"memory_kib"`
	Passes       uint32 `json:"passes"`
	Parallelism  uint8  `json:"parallelism"`
	TagLength    int    `json:"tag_length"`
	CounterMode  string `json:"counter_mode"`
	KeyPrefix    string `json:"key_prefix"`
	KeySignature string `json:"key_signature"`
}

type admissionChallengeResponse struct {
	Status           string             `json:"status"`
	TransactionID    string             `json:"transaction_id"`
	Classification   string             `json:"classification"`
	BindingStatement string             `json:"binding_statement"`
	ProofOfWork      admissionChallenge `json:"proof_of_work"`
	ExpiresAt        string             `json:"expires_at"`
}

type admissionSolution struct {
	Counter    string `json:"counter"`
	DerivedKey string `json:"derived_key"`
}

type admissionCompletionRequest struct {
	BindingStatement string            `json:"binding_statement"`
	ManagementProof  flattenedJWS      `json:"management_proof"`
	Operation        string            `json:"operation"`
	ProofOfWork      admissionSolution `json:"proof_of_work"`
	SessionProof     flattenedJWS      `json:"session_proof"`
	TransactionID    string            `json:"transaction_id"`
}

type sessionAuthorization struct {
	Status                  string   `json:"status"`
	Created                 bool     `json:"created"`
	PrincipalID             string   `json:"principal_id"`
	CredentialID            string   `json:"credential_id"`
	ClientID                string   `json:"client_id"`
	SessionID               string   `json:"session_id"`
	SessionCredentialID     string   `json:"session_credential_id"`
	SessionExpiresAt        string   `json:"session_expires_at"`
	Resource                string   `json:"resource"`
	GrantedScopes           []string `json:"granted_scopes"`
	TokenEndpoint           string   `json:"token_endpoint"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type admissionBindingInput struct {
	ProtocolVersion      string
	Operation            string
	Issuer               string
	Endpoint             string
	TransactionID        string
	ExpiresAt            int64
	ManagementThumbprint string
	SessionThumbprint    string
	RequestedScopes      []string
	Audience             string
	ServerNonce          string
	ClientNonce          string
	CreateIfMissing      bool
}

type solvedAdmission struct {
	counter    uint32
	derivedKey []byte
}

var admissionChallengeResponseFields = []string{
	"status",
	"transaction_id",
	"classification",
	"binding_statement",
	"proof_of_work",
	"expires_at",
}

var sessionAuthorizationFields = []string{
	"status",
	"created",
	"principal_id",
	"credential_id",
	"client_id",
	"session_id",
	"session_credential_id",
	"session_expires_at",
	"resource",
	"granted_scopes",
	"token_endpoint",
	"token_endpoint_auth_method",
}

func (client *Client) authorizeSession(
	ctx context.Context,
	metadata Metadata,
	management crypto.Signer,
	session *agentkey.SessionSigner,
) (sessionAuthorization, error) {
	managementJWK, err := publicJWK(management)
	if err != nil {
		return sessionAuthorization{}, err
	}
	sessionJWK, err := publicJWK(session)
	if err != nil {
		return sessionAuthorization{}, err
	}
	managementThumbprint, err := jwkThumbprint(managementJWK)
	if err != nil {
		return sessionAuthorization{}, err
	}
	sessionThumbprint, err := jwkThumbprint(sessionJWK)
	if err != nil || sessionThumbprint == managementThumbprint {
		return sessionAuthorization{}, newProtocolError(ErrProtocol, err)
	}
	nonce, err := client.fetchNonce(ctx, metadata.NonceEndpoint)
	if err != nil {
		return sessionAuthorization{}, err
	}
	clientNonce, err := client.randomBase64URL(16)
	if err != nil {
		return sessionAuthorization{}, err
	}
	jti, err := client.randomBase64URL(16)
	if err != nil {
		return sessionAuthorization{}, err
	}
	now := client.now().UTC()
	requestedScopes := []string{"search:read"}
	payload, err := json.Marshal(admissionStartPayload{
		Audience:         metadata.Resource,
		ClientNonce:      clientNonce,
		CreateIfMissing:  true,
		ExpiresAt:        now.Add(client.limits.MaxProofLifetime).Unix(),
		IssuedAt:         now.Unix(),
		Issuer:           metadata.Issuer,
		JTI:              jti,
		Operation:        admissionStartOperation,
		RequestedScopes:  requestedScopes,
		SessionPublicJWK: sessionJWK,
		Version:          ProtocolVersion,
	})
	if err != nil {
		return sessionAuthorization{}, newProtocolError(ErrProtocol, err)
	}
	startProof, err := signFlattenedJWS(client.random, management, payload, protectedHeader{
		Type:  admissionStartProofType,
		Alg:   "EdDSA",
		JWK:   &managementJWK,
		Nonce: nonce,
		URL:   metadata.AdmissionEndpoint,
	})
	if err != nil {
		return sessionAuthorization{}, err
	}
	startBody, err := json.Marshal(startProof)
	if err != nil {
		return sessionAuthorization{}, newProtocolError(ErrProtocol, err)
	}
	status, encoded, err := client.doJSON(
		ctx,
		http.MethodPost,
		metadata.AdmissionEndpoint,
		startBody,
		"application/jose+json",
	)
	if err != nil {
		return sessionAuthorization{}, err
	}
	if status != http.StatusAccepted {
		return sessionAuthorization{}, classifyAdmissionFailure(status, encoded)
	}
	var challengeResponse admissionChallengeResponse
	if err := decodeExactJSONObject(
		encoded,
		&challengeResponse,
		admissionChallengeResponseFields,
	); err != nil {
		return sessionAuthorization{}, newProtocolError(ErrProtocol, err)
	}
	binding, expiresAt, err := client.validateAdmissionChallenge(
		challengeResponse,
		admissionBindingInput{
			ProtocolVersion:      ProtocolVersion,
			Operation:            challengeResponse.Classification,
			Issuer:               metadata.Issuer,
			Endpoint:             metadata.AdmissionEndpoint,
			TransactionID:        challengeResponse.TransactionID,
			ManagementThumbprint: managementThumbprint,
			SessionThumbprint:    sessionThumbprint,
			RequestedScopes:      requestedScopes,
			Audience:             metadata.Resource,
			ServerNonce:          nonce,
			ClientNonce:          clientNonce,
			CreateIfMissing:      true,
		},
	)
	if err != nil {
		return sessionAuthorization{}, err
	}
	solution, err := solveAdmission(ctx, binding, challengeResponse.ProofOfWork, client.limits)
	if err != nil {
		return sessionAuthorization{}, err
	}
	defer clear(solution.derivedKey)
	if !client.now().UTC().Before(expiresAt) {
		return sessionAuthorization{}, newProtocolError(ErrChallengeExpired, nil)
	}
	managementProof, err := signFlattenedJWS(
		client.random,
		management,
		binding,
		protectedHeader{
			Type: admissionManagementProofType,
			Alg:  "EdDSA",
			JWK:  &managementJWK,
			URL:  metadata.AdmissionEndpoint,
		},
	)
	if err != nil {
		return sessionAuthorization{}, err
	}
	sessionProof, err := signFlattenedJWS(
		client.random,
		session,
		binding,
		protectedHeader{
			Type: admissionSessionProofType,
			Alg:  "EdDSA",
			JWK:  &sessionJWK,
			URL:  metadata.AdmissionEndpoint,
		},
	)
	if err != nil {
		return sessionAuthorization{}, err
	}
	completionBody, err := json.Marshal(admissionCompletionRequest{
		BindingStatement: challengeResponse.BindingStatement,
		ManagementProof:  managementProof,
		Operation:        admissionCompleteOperation,
		ProofOfWork: admissionSolution{
			Counter:    strconv.FormatUint(uint64(solution.counter), 10),
			DerivedKey: rawBase64URL.EncodeToString(solution.derivedKey),
		},
		SessionProof:  sessionProof,
		TransactionID: challengeResponse.TransactionID,
	})
	if err != nil {
		return sessionAuthorization{}, newProtocolError(ErrProtocol, err)
	}
	status, encoded, err = client.doJSON(
		ctx,
		http.MethodPost,
		metadata.AdmissionEndpoint,
		completionBody,
		"application/json",
	)
	clear(completionBody)
	if err != nil {
		return sessionAuthorization{}, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return sessionAuthorization{}, classifyAdmissionFailure(status, encoded)
	}
	var authorization sessionAuthorization
	if err := decodeExactJSONObject(encoded, &authorization, sessionAuthorizationFields); err != nil {
		return sessionAuthorization{}, newProtocolError(ErrProtocol, err)
	}
	if err := client.validateSessionAuthorization(
		status,
		authorization,
		metadata,
		requestedScopes,
	); err != nil {
		return sessionAuthorization{}, err
	}
	return authorization, nil
}

func (client *Client) validateAdmissionChallenge(
	response admissionChallengeResponse,
	expected admissionBindingInput,
) ([]byte, time.Time, error) {
	if response.Status != "challenge_required" ||
		(response.Classification != "enroll" && response.Classification != "login") ||
		!validPrefixedIdentifier(response.TransactionID, "atx_", 32) {
		return nil, time.Time{}, newProtocolError(ErrProtocol, nil)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, response.ExpiresAt)
	if err != nil || expiresAt.Nanosecond() != 0 {
		return nil, time.Time{}, newProtocolError(ErrProtocol, err)
	}
	now := client.now().UTC()
	if !expiresAt.After(now.Add(-client.limits.MaxClockSkew)) ||
		expiresAt.After(now.Add(client.limits.MaxChallengeLifetime+client.limits.MaxClockSkew)) {
		return nil, time.Time{}, newProtocolError(ErrChallengeExpired, nil)
	}
	expected.ExpiresAt = expiresAt.Unix()
	expected.Operation = response.Classification
	expectedBinding, err := encodeAdmissionBinding(expected)
	if err != nil {
		return nil, time.Time{}, err
	}
	binding, err := decodeBase64URL(response.BindingStatement, len(expectedBinding), len(expectedBinding))
	if err != nil || !bytes.Equal(binding, expectedBinding) {
		return nil, time.Time{}, newProtocolError(ErrProtocol, err)
	}
	if err := validateAdmissionParameters(response.ProofOfWork, client.limits); err != nil {
		return nil, time.Time{}, err
	}
	return binding, expiresAt, nil
}

func validateAdmissionParameters(challenge admissionChallenge, limits Limits) error {
	if challenge.Algorithm != argonAlgorithm ||
		challenge.ArgonVersion != argonVersion ||
		challenge.TagLength != argonTagLength ||
		challenge.CounterMode != argonCounterMode ||
		challenge.MemoryKiB < 8 ||
		challenge.MemoryKiB > limits.MaxArgonMemoryKiB ||
		challenge.Passes < 1 ||
		challenge.Passes > limits.MaxArgonPasses ||
		challenge.Parallelism < 1 ||
		challenge.Parallelism > limits.MaxArgonParallelism {
		return newProtocolError(ErrChallengeLimits, nil)
	}
	if _, err := decodeBase64URL(challenge.Salt, 16, 16); err != nil {
		return newProtocolError(ErrProtocol, err)
	}
	if _, err := decodeBase64URL(challenge.KeyPrefix, 16, 16); err != nil {
		return newProtocolError(ErrProtocol, err)
	}
	if _, err := decodeBase64URL(challenge.KeySignature, 32, 32); err != nil {
		return newProtocolError(ErrProtocol, err)
	}
	return nil
}

func solveAdmission(
	ctx context.Context,
	binding []byte,
	challenge admissionChallenge,
	limits Limits,
) (solvedAdmission, error) {
	if err := validateAdmissionParameters(challenge, limits); err != nil {
		return solvedAdmission{}, err
	}
	salt, _ := rawBase64URL.DecodeString(challenge.Salt)
	prefix, _ := rawBase64URL.DecodeString(challenge.KeyPrefix)
	bindingDigest := sha256.Sum256(binding)
	started := time.Now()
	for counter := uint32(0); counter < limits.MaxArgonAttempts; counter++ {
		if err := ctx.Err(); err != nil {
			return solvedAdmission{}, newProtocolError(ErrChallengeLimits, err)
		}
		if time.Since(started) >= limits.MaxArgonElapsed {
			return solvedAdmission{}, newProtocolError(ErrChallengeLimits, nil)
		}
		derivedKey := deriveAdmissionKey(
			bindingDigest,
			salt,
			challenge,
			counter,
		)
		if time.Since(started) > limits.MaxArgonElapsed {
			clear(derivedKey)
			return solvedAdmission{}, newProtocolError(ErrChallengeLimits, nil)
		}
		if subtle.ConstantTimeCompare(derivedKey[:16], prefix) == 1 {
			return solvedAdmission{counter: counter, derivedKey: derivedKey}, nil
		}
		clear(derivedKey)
	}
	return solvedAdmission{}, newProtocolError(ErrChallengeLimits, nil)
}

func deriveAdmissionKey(
	bindingDigest [sha256.Size]byte,
	salt []byte,
	challenge admissionChallenge,
	counter uint32,
) []byte {
	message := make([]byte, 0, len(argonDomain)+sha256.Size+4)
	message = append(message, argonDomain...)
	message = append(message, bindingDigest[:]...)
	counterBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(counterBytes, counter)
	message = append(message, counterBytes...)
	derivedKey := argon2.IDKey(
		message,
		salt,
		challenge.Passes,
		challenge.MemoryKiB,
		challenge.Parallelism,
		argonTagLength,
	)
	clear(message)
	return derivedKey
}

func encodeAdmissionBinding(input admissionBindingInput) ([]byte, error) {
	scopes := append([]string(nil), input.RequestedScopes...)
	sort.Strings(scopes)
	fields := [][2]string{
		{"protocol_version", input.ProtocolVersion},
		{"operation", input.Operation},
		{"issuer", input.Issuer},
		{"endpoint", input.Endpoint},
		{"transaction_id", input.TransactionID},
		{"expires_at", strconv.FormatInt(input.ExpiresAt, 10)},
		{"management_thumbprint", input.ManagementThumbprint},
		{"session_thumbprint", input.SessionThumbprint},
	}
	for _, scope := range scopes {
		fields = append(fields, [2]string{"requested_scope", scope})
	}
	fields = append(fields,
		[2]string{"audience", input.Audience},
		[2]string{"server_nonce", input.ServerNonce},
		[2]string{"client_nonce", input.ClientNonce},
		[2]string{"create_if_missing", strconv.FormatBool(input.CreateIfMissing)},
	)
	var encoded bytes.Buffer
	encoded.WriteString(admissionBindingDomain)
	encoded.WriteByte(0)
	if err := binary.Write(&encoded, binary.BigEndian, uint32(len(fields))); err != nil {
		return nil, newProtocolError(ErrProtocol, err)
	}
	for _, field := range fields {
		name := []byte(field[0])
		value := []byte(field[1])
		if len(name) > 0xffff || uint64(len(value)) > uint64(^uint32(0)) {
			return nil, newProtocolError(ErrProtocol, nil)
		}
		if err := binary.Write(&encoded, binary.BigEndian, uint16(len(name))); err != nil {
			return nil, newProtocolError(ErrProtocol, err)
		}
		encoded.Write(name)
		if err := binary.Write(&encoded, binary.BigEndian, uint32(len(value))); err != nil {
			return nil, newProtocolError(ErrProtocol, err)
		}
		encoded.Write(value)
	}
	return encoded.Bytes(), nil
}

func (client *Client) validateSessionAuthorization(
	status int,
	authorization sessionAuthorization,
	metadata Metadata,
	requestedScopes []string,
) error {
	sessionExpiresAt, err := time.Parse(time.RFC3339Nano, authorization.SessionExpiresAt)
	if err != nil {
		return newProtocolError(ErrProtocol, err)
	}
	now := client.now().UTC()
	if authorization.Status != "active" ||
		(status == http.StatusOK && authorization.Created) ||
		(status == http.StatusCreated && !authorization.Created) ||
		!validPrefixedIdentifier(authorization.PrincipalID, "agt_", 32) ||
		!validPrefixedIdentifier(authorization.CredentialID, "acred_", 32) ||
		!validPrefixedIdentifier(authorization.ClientID, "clt_", 32) ||
		!validPrefixedIdentifier(authorization.SessionID, "ses_", 32) ||
		!validPrefixedIdentifier(authorization.SessionCredentialID, "scred_", 32) ||
		authorization.Resource != metadata.Resource ||
		!equalOrderedStrings(authorization.GrantedScopes, requestedScopes) ||
		authorization.TokenEndpoint != metadata.TokenEndpoint ||
		authorization.TokenEndpointAuthMethod != "private_key_jwt" ||
		!sessionExpiresAt.After(now.Add(-client.limits.MaxClockSkew)) ||
		sessionExpiresAt.After(now.Add(client.limits.MaxSessionLifetime+client.limits.MaxClockSkew)) {
		return newProtocolError(ErrProtocol, nil)
	}
	return nil
}

func classifyAdmissionFailure(status int, encoded []byte) error {
	var response errorResponse
	if err := decodeExactJSONObject(encoded, &response, errorResponseFields); err != nil {
		return newProtocolError(ErrAuthentication, err)
	}
	if response.Error == "" || response.ErrorDescription == "" || len(response.ErrorDescription) > 1024 {
		return newProtocolError(ErrAuthentication, nil)
	}
	switch response.Error {
	case "transaction_expired":
		if status == http.StatusGone {
			return newProtocolError(ErrChallengeExpired, nil)
		}
	case "agent_not_found":
		if status == http.StatusNotFound {
			return newProtocolError(ErrAgentNotFound, nil)
		}
	}
	return newProtocolError(ErrAuthentication, nil)
}

func validPrefixedIdentifier(value, prefix string, suffixLength int) bool {
	if len(value) != len(prefix)+suffixLength || value[:len(prefix)] != prefix {
		return false
	}
	for _, character := range value[len(prefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func equalOrderedStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
