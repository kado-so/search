package agentauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kado-so/search/internal/agentkey"
)

type goal4Transaction struct {
	binding      []byte
	management   PublicJWK
	session      PublicJWK
	challenge    admissionChallenge
	created      bool
	principalID  string
	credentialID string
	clientID     string
}

type goal4Server struct {
	server          *httptest.Server
	mu              sync.Mutex
	nonce           string
	nonceConsumed   bool
	transaction     *goal4Transaction
	tokenPrivate    ed25519.PrivateKey
	tamperAudience  bool
	excessiveMemory bool
	assertionJTIs   map[string]bool
}

func newGoal4Server() *goal4Server {
	seed := bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	fake := &goal4Server{
		nonce:         rawBase64URL.EncodeToString(bytes.Repeat([]byte{1}, 24)),
		tokenPrivate:  ed25519.NewKeyFromSeed(seed),
		assertionJTIs: make(map[string]bool),
	}
	fake.server = httptest.NewTLSServer(http.HandlerFunc(fake.handle))
	return fake
}

func (fake *goal4Server) close() {
	fake.server.Close()
	clear(fake.tokenPrivate)
}

func (fake *goal4Server) issuer() string {
	return fake.server.URL
}

func (fake *goal4Server) handle(response http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet &&
		request.URL.Path == "/.well-known/oauth-protected-resource":
		fake.sendJSON(response, http.StatusOK, protectedResourceMetadata{
			Resource:                  fake.issuer(),
			AuthorizationServers:      []string{fake.issuer()},
			ScopesSupported:           []string{"search:read"},
			BearerMethodsSupported:    []string{"header"},
			AgentPrincipalMetadataURI: fake.issuer() + "/.well-known/agent-principal",
		})
	case request.Method == http.MethodGet &&
		request.URL.Path == "/.well-known/oauth-authorization-server":
		fake.sendJSON(response, http.StatusOK, authorizationMetadata{
			Issuer:                    fake.issuer(),
			TokenEndpoint:             fake.issuer() + "/oauth/token",
			JWKSURI:                   fake.issuer() + "/.well-known/jwks.json",
			TokenAuthMethods:          []string{"private_key_jwt"},
			GrantTypes:                []string{"client_credentials"},
			ProtocolVersions:          []string{ProtocolVersion},
			NonceEndpoint:             fake.issuer() + "/api/auth/agent/nonce",
			EnrollmentEndpoint:        fake.issuer() + "/api/auth/agent/enroll",
			AdmissionEndpoint:         fake.issuer() + "/api/auth/agent/enroll/admission",
			CredentialEndpoint:        fake.issuer() + "/api/auth/agent/credentials",
			AutonomousEnrollment:      boolPointer(true),
			KeyAlgorithms:             []string{"Ed25519"},
			JWSAlgorithms:             []string{"EdDSA"},
			AdmissionChallengeTypes:   []string{ProofAlgorithm},
			AgentPrincipalMetadataURI: fake.issuer() + "/.well-known/agent-principal",
		})
	case request.Method == http.MethodGet &&
		request.URL.Path == "/.well-known/agent-principal":
		fake.sendJSON(response, http.StatusOK, agentProtocolMetadata{
			Issuer:                  fake.issuer(),
			ProtectedResource:       fake.issuer(),
			ProtocolVersions:        []string{ProtocolVersion},
			NonceEndpoint:           fake.issuer() + "/api/auth/agent/nonce",
			EnrollmentEndpoint:      fake.issuer() + "/api/auth/agent/enroll",
			AdmissionEndpoint:       fake.issuer() + "/api/auth/agent/enroll/admission",
			CredentialEndpoint:      fake.issuer() + "/api/auth/agent/credentials",
			AutonomousEnrollment:    boolPointer(true),
			KeyAlgorithms:           []string{"Ed25519"},
			JWSAlgorithms:           []string{"EdDSA"},
			AdmissionChallengeTypes: []string{ProofAlgorithm},
		})
	case request.Method == http.MethodHead && request.URL.Path == "/api/auth/agent/nonce":
		response.Header().Set("Replay-Nonce", fake.nonce)
		response.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodPost &&
		request.URL.Path == "/api/auth/agent/enroll/admission" &&
		request.Header.Get("Content-Type") == "application/jose+json":
		fake.startAdmission(response, request)
	case request.Method == http.MethodPost &&
		request.URL.Path == "/api/auth/agent/enroll/admission" &&
		request.Header.Get("Content-Type") == "application/json":
		fake.completeAdmission(response, request)
	case request.Method == http.MethodPost && request.URL.Path == "/oauth/token":
		fake.exchangeToken(response, request)
	case request.Method == http.MethodGet && request.URL.Path == "/.well-known/jwks.json":
		fake.sendJWKS(response)
	default:
		fake.sendAgentError(response, http.StatusNotFound, "not_found")
	}
}

func (fake *goal4Server) startAdmission(response http.ResponseWriter, request *http.Request) {
	encoded, err := io.ReadAll(io.LimitReader(request.Body, 16*1024+1))
	if err != nil || len(encoded) > 16*1024 {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	var proof flattenedJWS
	if err := decodeExactJSONObject(
		encoded,
		&proof,
		[]string{"protected", "payload", "signature"},
	); err != nil {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_proof")
		return
	}
	protected, protectedErr := rawBase64URL.DecodeString(proof.Protected)
	payloadBytes, payloadErr := rawBase64URL.DecodeString(proof.Payload)
	if protectedErr != nil || payloadErr != nil {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_proof")
		return
	}
	var header protectedHeader
	if err := decodeExactJSONObject(
		protected,
		&header,
		[]string{"alg", "jwk", "nonce", "typ", "url"},
	); err != nil ||
		header.Nonce != fake.nonce {
		fake.sendAgentError(response, http.StatusBadRequest, "bad_nonce")
		return
	}
	fake.mu.Lock()
	if fake.nonceConsumed {
		fake.mu.Unlock()
		fake.sendAgentError(response, http.StatusBadRequest, "bad_nonce")
		return
	}
	fake.nonceConsumed = true
	fake.mu.Unlock()
	management, err := verifyFlattenedJWS(
		proof,
		admissionStartProofType,
		fake.issuer()+"/api/auth/agent/enroll/admission",
		payloadBytes,
	)
	if err != nil {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_proof")
		return
	}
	var payload admissionStartPayload
	if err := decodeExactJSONObject(
		payloadBytes,
		&payload,
		[]string{
			"audience",
			"client_nonce",
			"create_if_missing",
			"exp",
			"iat",
			"issuer",
			"jti",
			"operation",
			"requested_scopes",
			"session_public_jwk",
			"version",
		},
	); err != nil ||
		payload.Version != ProtocolVersion ||
		payload.Operation != admissionStartOperation ||
		payload.Issuer != fake.issuer() ||
		payload.Audience != fake.issuer() ||
		!payload.CreateIfMissing ||
		!equalOrderedStrings(payload.RequestedScopes, []string{"search:read"}) {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	managementThumbprint, _ := jwkThumbprint(management)
	sessionThumbprint, err := jwkThumbprint(payload.SessionPublicJWK)
	if err != nil || sessionThumbprint == managementThumbprint {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	expiresAt := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	transactionID := "atx_00000000000000000000000000000001"
	binding, err := encodeAdmissionBinding(admissionBindingInput{
		ProtocolVersion:      ProtocolVersion,
		Operation:            "enroll",
		Issuer:               fake.issuer(),
		Endpoint:             fake.issuer() + "/api/auth/agent/enroll/admission",
		TransactionID:        transactionID,
		ExpiresAt:            expiresAt.Unix(),
		ManagementThumbprint: managementThumbprint,
		SessionThumbprint:    sessionThumbprint,
		RequestedScopes:      payload.RequestedScopes,
		Audience:             payload.Audience,
		ServerNonce:          fake.nonce,
		ClientNonce:          payload.ClientNonce,
		CreateIfMissing:      true,
	})
	if err != nil {
		fake.sendAgentError(response, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	memory := uint32(8)
	if fake.excessiveMemory {
		memory = 128*1024 + 1
	}
	challenge := admissionChallenge{
		Algorithm:    argonAlgorithm,
		ArgonVersion: argonVersion,
		Salt:         rawBase64URL.EncodeToString(bytes.Repeat([]byte{3}, 16)),
		MemoryKiB:    memory,
		Passes:       1,
		Parallelism:  1,
		TagLength:    argonTagLength,
		CounterMode:  argonCounterMode,
		KeySignature: rawBase64URL.EncodeToString(bytes.Repeat([]byte{7}, 32)),
	}
	if !fake.excessiveMemory {
		digest := sha256.Sum256(binding)
		key := deriveAdmissionKey(digest, bytes.Repeat([]byte{3}, 16), challenge, 0)
		challenge.KeyPrefix = rawBase64URL.EncodeToString(key[:16])
		clear(key)
	} else {
		challenge.KeyPrefix = rawBase64URL.EncodeToString(bytes.Repeat([]byte{4}, 16))
	}
	fake.mu.Lock()
	fake.transaction = &goal4Transaction{
		binding:      binding,
		management:   management,
		session:      payload.SessionPublicJWK,
		challenge:    challenge,
		created:      true,
		principalID:  "agt_00000000000000000000000000000001",
		credentialID: "acred_00000000000000000000000000000001",
		clientID:     "clt_00000000000000000000000000000001",
	}
	fake.mu.Unlock()
	fake.sendJSON(response, http.StatusAccepted, admissionChallengeResponse{
		Status:           "challenge_required",
		TransactionID:    transactionID,
		Classification:   "enroll",
		BindingStatement: rawBase64URL.EncodeToString(binding),
		ProofOfWork:      challenge,
		ExpiresAt:        expiresAt.Format(time.RFC3339Nano),
	})
}

func (fake *goal4Server) completeAdmission(response http.ResponseWriter, request *http.Request) {
	encoded, err := io.ReadAll(io.LimitReader(request.Body, 48*1024+1))
	if err != nil {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	var completion admissionCompletionRequest
	if err := decodeExactJSONObject(
		encoded,
		&completion,
		[]string{
			"binding_statement",
			"management_proof",
			"operation",
			"proof_of_work",
			"session_proof",
			"transaction_id",
		},
	); err != nil {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	fake.mu.Lock()
	transaction := fake.transaction
	fake.mu.Unlock()
	if transaction == nil ||
		completion.Operation != admissionCompleteOperation ||
		completion.TransactionID != "atx_00000000000000000000000000000001" ||
		completion.BindingStatement != rawBase64URL.EncodeToString(transaction.binding) {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_challenge")
		return
	}
	digest := sha256.Sum256(transaction.binding)
	expectedKey := deriveAdmissionKey(
		digest,
		bytes.Repeat([]byte{3}, 16),
		transaction.challenge,
		0,
	)
	defer clear(expectedKey)
	if completion.ProofOfWork.Counter != "0" ||
		completion.ProofOfWork.DerivedKey != rawBase64URL.EncodeToString(expectedKey) {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_challenge")
		return
	}
	management, managementErr := verifyFlattenedJWS(
		completion.ManagementProof,
		admissionManagementProofType,
		fake.issuer()+"/api/auth/agent/enroll/admission",
		transaction.binding,
	)
	session, sessionErr := verifyFlattenedJWS(
		completion.SessionProof,
		admissionSessionProofType,
		fake.issuer()+"/api/auth/agent/enroll/admission",
		transaction.binding,
	)
	if managementErr != nil ||
		sessionErr != nil ||
		management != transaction.management ||
		session != transaction.session {
		fake.sendAgentError(response, http.StatusBadRequest, "invalid_proof")
		return
	}
	fake.sendJSON(response, http.StatusCreated, sessionAuthorization{
		Status:                  "active",
		Created:                 true,
		PrincipalID:             transaction.principalID,
		CredentialID:            transaction.credentialID,
		ClientID:                transaction.clientID,
		SessionID:               "ses_00000000000000000000000000000001",
		SessionCredentialID:     "scred_00000000000000000000000000000001",
		SessionExpiresAt:        time.Now().UTC().Truncate(time.Second).Add(15 * time.Minute).Format(time.RFC3339Nano),
		Resource:                fake.issuer(),
		GrantedScopes:           []string{"search:read"},
		TokenEndpoint:           fake.issuer() + "/oauth/token",
		TokenEndpointAuthMethod: "private_key_jwt",
	})
}

func (fake *goal4Server) exchangeToken(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
		request.ParseForm() != nil {
		fake.sendOAuthError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	expectedFields := []string{
		"client_assertion",
		"client_assertion_type",
		"client_id",
		"grant_type",
		"resource",
		"scope",
	}
	keys := make([]string, 0, len(request.PostForm))
	for key, values := range request.PostForm {
		if len(values) != 1 {
			fake.sendOAuthError(response, http.StatusBadRequest, "invalid_request")
			return
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !equalOrderedStrings(keys, expectedFields) ||
		request.PostForm.Get("client_assertion_type") != privateKeyJWTAssertionType ||
		request.PostForm.Get("client_id") != "clt_00000000000000000000000000000001" ||
		request.PostForm.Get("grant_type") != "client_credentials" ||
		request.PostForm.Get("resource") != fake.issuer() ||
		request.PostForm.Get("scope") != "search:read" {
		fake.sendOAuthError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	parts := strings.Split(request.PostForm.Get("client_assertion"), ".")
	if len(parts) != 3 {
		fake.sendOAuthError(response, http.StatusUnauthorized, "invalid_client")
		return
	}
	protected, protectedErr := rawBase64URL.DecodeString(parts[0])
	payload, payloadErr := rawBase64URL.DecodeString(parts[1])
	signature, signatureErr := rawBase64URL.DecodeString(parts[2])
	var header privateKeyJWTHeader
	var claims privateKeyJWTClaims
	if protectedErr != nil ||
		payloadErr != nil ||
		signatureErr != nil ||
		decodeExactJSONObject(protected, &header, []string{"alg", "kid", "typ"}) != nil ||
		decodeExactJSONObject(payload, &claims, []string{"aud", "exp", "iat", "iss", "jti", "sub"}) != nil ||
		header != (privateKeyJWTHeader{
			Algorithm: "EdDSA",
			KeyID:     "scred_00000000000000000000000000000001",
			Type:      "JWT",
		}) ||
		claims.Issuer != "clt_00000000000000000000000000000001" ||
		claims.Subject != claims.Issuer ||
		claims.Audience != fake.issuer()+"/oauth/token" ||
		claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > 60 {
		fake.sendOAuthError(response, http.StatusUnauthorized, "invalid_client")
		return
	}
	fake.mu.Lock()
	transaction := fake.transaction
	replayed := fake.assertionJTIs[claims.JTI]
	fake.assertionJTIs[claims.JTI] = true
	fake.mu.Unlock()
	sessionPublic, err := validatePublicJWK(transaction.session)
	if err != nil ||
		replayed ||
		!ed25519.Verify(sessionPublic, []byte(parts[0]+"."+parts[1]), signature) {
		fake.sendOAuthError(response, http.StatusUnauthorized, "invalid_client")
		return
	}
	now := time.Now().Unix()
	audience := fake.issuer()
	if fake.tamperAudience {
		audience = fake.issuer() + "/other"
	}
	accessClaims := accessTokenClaims{
		Issuer:         fake.issuer(),
		Subject:        transaction.principalID,
		Audience:       audience,
		ClientID:       transaction.clientID,
		PrincipalType:  "agent",
		AgentSessionID: "ses_00000000000000000000000000000001",
		SessionMode:    "autonomous",
		Scope:          "search:read",
		IssuedAt:       now,
		ExpiresAt:      now + 300,
		JTI:            "00000000-0000-4000-8000-000000000001",
	}
	token := fake.signAccessToken(accessClaims)
	fake.sendJSON(response, http.StatusOK, tokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   300,
		Scope:       "search:read",
	})
}

func (fake *goal4Server) signAccessToken(claims accessTokenClaims) string {
	protected, _ := json.Marshal(accessTokenHeader{
		Algorithm: "EdDSA",
		KeyID:     "agent-token-test",
		Type:      "at+jwt",
	})
	payload, _ := json.Marshal(claims)
	protectedText := rawBase64URL.EncodeToString(protected)
	payloadText := rawBase64URL.EncodeToString(payload)
	input := protectedText + "." + payloadText
	signature := ed25519.Sign(fake.tokenPrivate, []byte(input))
	return input + "." + rawBase64URL.EncodeToString(signature)
}

func (fake *goal4Server) sendJWKS(response http.ResponseWriter) {
	public := fake.tokenPrivate.Public().(ed25519.PublicKey)
	fake.sendJSON(response, http.StatusOK, tokenJWKS{Keys: []tokenSigningJWK{{
		Algorithm: "EdDSA",
		Curve:     "Ed25519",
		KeyID:     "agent-token-test",
		KeyType:   "OKP",
		Use:       "sig",
		X:         rawBase64URL.EncodeToString(public),
	}}})
}

func (fake *goal4Server) sendAgentError(
	response http.ResponseWriter,
	status int,
	code string,
) {
	fake.sendJSON(response, status, errorResponse{
		Error:            code,
		ErrorDescription: "A safe server description.",
		Retryable:        false,
	})
}

func (fake *goal4Server) sendOAuthError(
	response http.ResponseWriter,
	status int,
	code string,
) {
	fake.sendJSON(response, status, struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}{
		Error:            code,
		ErrorDescription: "A safe OAuth description.",
	})
}

func (fake *goal4Server) sendJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func TestAcquireTokenCompletesAdmissionAndVerifiesAccessJWT(t *testing.T) {
	t.Parallel()

	server := newGoal4Server()
	defer server.close()
	base, _ := url.Parse(server.issuer())
	client, err := NewClient(
		base,
		server.server.Client(),
		DefaultLimits(),
		bytes.NewReader(deterministicBytes(8192)),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	token, err := client.AcquireToken(
		context.Background(),
		&memoryKeyStore{},
		Request{Mode: CreateIfMissing},
	)
	if err != nil {
		t.Fatalf("AcquireToken() error = %v", err)
	}
	if token.PrincipalID != "agt_00000000000000000000000000000001" ||
		token.SessionID != "ses_00000000000000000000000000000001" ||
		token.AuthorizationHeader() == "" ||
		!equalOrderedStrings(token.Scopes, []string{"search:read"}) {
		t.Fatalf("AcquireToken() = %#v", token)
	}
	for _, rendered := range []string{
		fmt.Sprintf("%v", token),
		fmt.Sprintf("%+v", token),
		fmt.Sprintf("%#v", token),
	} {
		if strings.Contains(rendered, token.accessToken) {
			t.Fatalf("formatted SessionToken exposed bearer credential: %q", rendered)
		}
	}
}

func TestAcquireTokenRejectsExcessiveChallengeAndTamperedAccessAudience(t *testing.T) {
	t.Parallel()

	t.Run("challenge memory", func(t *testing.T) {
		server := newGoal4Server()
		defer server.close()
		server.excessiveMemory = true
		client := newGoal4Client(t, server)
		if _, err := client.AcquireToken(
			context.Background(),
			&memoryKeyStore{},
			Request{Mode: CreateIfMissing},
		); !errors.Is(err, ErrChallengeLimits) {
			t.Fatalf("AcquireToken(excessive challenge) error = %v", err)
		}
	})

	t.Run("access audience", func(t *testing.T) {
		server := newGoal4Server()
		defer server.close()
		server.tamperAudience = true
		client := newGoal4Client(t, server)
		if _, err := client.AcquireToken(
			context.Background(),
			&memoryKeyStore{},
			Request{Mode: CreateIfMissing},
		); !errors.Is(err, ErrProtocol) {
			t.Fatalf("AcquireToken(tampered audience) error = %v", err)
		}
	})
}

func TestTokenExchangeUsesFreshAssertionJTI(t *testing.T) {
	t.Parallel()

	server := newGoal4Server()
	defer server.close()
	client := newGoal4Client(t, server)
	metadata, err := client.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	management, err := agentkey.GenerateManagementSigner()
	if err != nil {
		t.Fatalf("GenerateManagementSigner() error = %v", err)
	}
	session, err := agentkey.GenerateSessionSigner()
	if err != nil {
		t.Fatalf("GenerateSessionSigner() error = %v", err)
	}
	authorization, err := client.authorizeSession(
		context.Background(),
		metadata,
		management,
		session,
	)
	if err != nil {
		t.Fatalf("authorizeSession() error = %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := client.exchangeToken(
			context.Background(),
			metadata,
			authorization,
			session,
		); err != nil {
			t.Fatalf("exchangeToken(attempt %d) error = %v", attempt, err)
		}
	}
	server.mu.Lock()
	jtiCount := len(server.assertionJTIs)
	server.mu.Unlock()
	if jtiCount != 2 {
		t.Fatalf("recorded assertion JTIs = %d, want 2", jtiCount)
	}
}

func newGoal4Client(t *testing.T, server *goal4Server) *Client {
	t.Helper()
	base, err := url.Parse(server.issuer())
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	client, err := NewClient(
		base,
		server.server.Client(),
		DefaultLimits(),
		bytes.NewReader(deterministicBytes(8192)),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
