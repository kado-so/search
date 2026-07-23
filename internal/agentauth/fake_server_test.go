package agentauth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/kado-so/search/internal/keystore"
)

type fakePersistentState struct {
	mu         sync.Mutex
	principals map[string]fakePrincipal
	next       int
}

type fakePrincipal struct {
	principalID  string
	credentialID string
	clientID     string
}

func newFakePersistentState() *fakePersistentState {
	return &fakePersistentState{principals: make(map[string]fakePrincipal)}
}

func (state *fakePersistentState) resolve(
	thumbprint string,
	create bool,
) (fakePrincipal, bool, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if principal, found := state.principals[thumbprint]; found {
		return principal, false, true
	}
	if !create {
		return fakePrincipal{}, false, false
	}
	state.next++
	suffix := integerString(state.next)
	principal := fakePrincipal{
		principalID:  "agt_" + suffix,
		credentialID: "acred_" + suffix,
		clientID:     "clt_" + suffix,
	}
	state.principals[thumbprint] = principal
	return principal, true, true
}

type fakeAuthServer struct {
	server            *httptest.Server
	persistent        *fakePersistentState
	mu                sync.Mutex
	nonces            map[string]bool
	jtis              map[string]bool
	nextNonce         int
	reuseNonce        bool
	admissionRequired bool
	issuerValue       string
	nonceURL          string
	extensionMismatch bool
	resultTokenURL    string
	resultStatus      int
}

func newFakeAuthServer(state *fakePersistentState) *fakeAuthServer {
	fake := &fakeAuthServer{
		persistent: state,
		nonces:     make(map[string]bool),
		jtis:       make(map[string]bool),
	}
	fake.server = httptest.NewTLSServer(http.HandlerFunc(fake.handle))
	return fake
}

func (fake *fakeAuthServer) close() {
	fake.server.Close()
}

func (fake *fakeAuthServer) restart() {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.nonces = make(map[string]bool)
	fake.jtis = make(map[string]bool)
	fake.nextNonce = 0
}

func (fake *fakeAuthServer) issuer() string {
	return fake.server.URL
}

func (fake *fakeAuthServer) handle(response http.ResponseWriter, request *http.Request) {
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
		issuer := fake.issuerValue
		if issuer == "" {
			issuer = fake.issuer()
		}
		nonceEndpoint := fake.nonceURL
		if nonceEndpoint == "" {
			nonceEndpoint = fake.issuer() + "/api/auth/agent/nonce"
		}
		fake.sendJSON(response, http.StatusOK, authorizationMetadata{
			Issuer:                    issuer,
			TokenEndpoint:             fake.issuer() + "/oauth/token",
			JWKSURI:                   fake.issuer() + "/.well-known/jwks.json",
			TokenAuthMethods:          []string{"private_key_jwt"},
			GrantTypes:                []string{"client_credentials"},
			ProtocolVersions:          []string{ProtocolVersion},
			NonceEndpoint:             nonceEndpoint,
			EnrollmentEndpoint:        fake.issuer() + "/api/auth/agent/enroll",
			CredentialEndpoint:        fake.issuer() + "/api/auth/agent/credentials",
			AutonomousEnrollment:      boolPointer(true),
			KeyAlgorithms:             []string{"Ed25519"},
			JWSAlgorithms:             []string{"EdDSA"},
			AdmissionChallengeTypes:   []string{ProofAlgorithm},
			AgentPrincipalMetadataURI: fake.issuer() + "/.well-known/agent-principal",
		})
	case request.Method == http.MethodGet &&
		request.URL.Path == "/.well-known/agent-principal":
		nonceEndpoint := fake.issuer() + "/api/auth/agent/nonce"
		if fake.extensionMismatch {
			nonceEndpoint = fake.issuer() + "/api/auth/agent/other-nonce"
		}
		fake.sendJSON(response, http.StatusOK, agentProtocolMetadata{
			Issuer:                  fake.issuer(),
			ProtectedResource:       fake.issuer(),
			ProtocolVersions:        []string{ProtocolVersion},
			NonceEndpoint:           nonceEndpoint,
			EnrollmentEndpoint:      fake.issuer() + "/api/auth/agent/enroll",
			CredentialEndpoint:      fake.issuer() + "/api/auth/agent/credentials",
			AutonomousEnrollment:    boolPointer(true),
			KeyAlgorithms:           []string{"Ed25519"},
			JWSAlgorithms:           []string{"EdDSA"},
			AdmissionChallengeTypes: []string{ProofAlgorithm},
		})
	case (request.Method == http.MethodHead || request.Method == http.MethodGet) &&
		request.URL.Path == "/api/auth/agent/nonce":
		fake.handleNonce(response)
	case request.Method == http.MethodPost &&
		request.URL.Path == "/api/auth/agent/enroll":
		fake.handleEnrollment(response, request)
	default:
		fake.sendError(response, http.StatusNotFound, "not_found", false)
	}
}

func (fake *fakeAuthServer) handleNonce(response http.ResponseWriter) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.nextNonce++
	number := fake.nextNonce
	if fake.reuseNonce {
		number = 1
	}
	nonce := rawBase64URL.EncodeToString(bytes.Repeat([]byte{byte(number)}, 24))
	if _, found := fake.nonces[nonce]; !found {
		fake.nonces[nonce] = false
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Replay-Nonce", nonce)
	response.WriteHeader(http.StatusNoContent)
}

func (fake *fakeAuthServer) handleEnrollment(
	response http.ResponseWriter,
	request *http.Request,
) {
	if request.Header.Get("Content-Type") != "application/jose+json" {
		fake.sendError(response, http.StatusBadRequest, "invalid_request", false)
		return
	}
	encoded, err := io.ReadAll(io.LimitReader(request.Body, 16*1024+1))
	if err != nil || len(encoded) > 16*1024 {
		fake.sendError(response, http.StatusBadRequest, "invalid_request", false)
		return
	}
	var jws flattenedJWS
	if err := decodeStrictJSON(encoded, &jws, true); err != nil {
		fake.sendError(response, http.StatusBadRequest, "invalid_proof", false)
		return
	}
	protectedBytes, protectedErr := rawBase64URL.DecodeString(jws.Protected)
	payloadBytes, payloadErr := rawBase64URL.DecodeString(jws.Payload)
	if protectedErr != nil || payloadErr != nil {
		fake.sendError(response, http.StatusBadRequest, "invalid_proof", false)
		return
	}
	var header protectedHeader
	if err := decodeStrictJSON(protectedBytes, &header, true); err != nil {
		fake.sendError(response, http.StatusBadRequest, "invalid_proof", false)
		return
	}
	fake.mu.Lock()
	consumed, found := fake.nonces[header.Nonce]
	if found && !consumed {
		fake.nonces[header.Nonce] = true
	}
	fake.mu.Unlock()
	if !found || consumed {
		fake.sendError(response, http.StatusBadRequest, "bad_nonce", true)
		return
	}
	jwk, err := verifyFlattenedJWS(
		jws,
		enrollmentProofType,
		fake.issuer()+"/api/auth/agent/enroll",
		payloadBytes,
	)
	if err != nil {
		fake.sendError(response, http.StatusBadRequest, "invalid_proof", false)
		return
	}
	var payload enrollmentPayload
	now := time.Now().Unix()
	if err := decodeStrictJSON(payloadBytes, &payload, true); err != nil ||
		payload.Version != ProtocolVersion ||
		payload.Operation != enrollmentOperation ||
		payload.Issuer != fake.issuer() ||
		payload.ExpiresAt <= payload.IssuedAt ||
		payload.ExpiresAt-payload.IssuedAt > 60 ||
		payload.IssuedAt > now+30 ||
		payload.ExpiresAt < now {
		fake.sendError(response, http.StatusBadRequest, "invalid_request", false)
		return
	}
	if _, err := decodeBase64URL(payload.JTI, 12, 64); err != nil {
		fake.sendError(response, http.StatusBadRequest, "invalid_proof", false)
		return
	}
	if _, err := decodeBase64URL(payload.ClientNonce, 16, 64); err != nil {
		fake.sendError(response, http.StatusBadRequest, "invalid_request", false)
		return
	}
	fake.mu.Lock()
	replayed := fake.jtis[payload.JTI]
	fake.jtis[payload.JTI] = true
	fake.mu.Unlock()
	if replayed {
		fake.sendError(response, http.StatusBadRequest, "proof_replayed", false)
		return
	}
	thumbprint, err := jwkThumbprint(jwk)
	if err != nil {
		fake.sendError(response, http.StatusBadRequest, "invalid_jwk", false)
		return
	}
	_, _, known := fake.persistent.resolve(thumbprint, false)
	if !known && !payload.CreateIfMissing {
		fake.sendError(response, http.StatusNotFound, "agent_not_found", false)
		return
	}
	if !known && fake.admissionRequired {
		fake.sendError(response, http.StatusForbidden, "admission_required", false)
		return
	}
	principal, created, _ := fake.persistent.resolve(thumbprint, payload.CreateIfMissing)
	tokenEndpoint := fake.resultTokenURL
	if tokenEndpoint == "" {
		tokenEndpoint = fake.issuer() + "/oauth/token"
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	if fake.resultStatus != 0 {
		status = fake.resultStatus
	}
	fake.sendJSON(response, status, successResponse{
		Status:                  "active",
		Created:                 created,
		PrincipalID:             principal.principalID,
		CredentialID:            principal.credentialID,
		ClientID:                principal.clientID,
		TokenEndpoint:           tokenEndpoint,
		TokenEndpointAuthMethod: "private_key_jwt",
	})
}

func (fake *fakeAuthServer) sendError(
	response http.ResponseWriter,
	status int,
	code string,
	retryable bool,
) {
	fake.sendJSON(response, status, struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		Retryable        bool   `json:"retryable"`
	}{
		Error:            code,
		ErrorDescription: "A safe server description.",
		Retryable:        retryable,
	})
}

func (fake *fakeAuthServer) sendJSON(
	response http.ResponseWriter,
	status int,
	value any,
) {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(encoded)
}

type memoryKeyStore struct {
	mu      sync.Mutex
	payload []byte
}

func (store *memoryKeyStore) Load() ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.payload == nil {
		return nil, keystore.ErrNotFound
	}
	return append([]byte(nil), store.payload...), nil
}

func (store *memoryKeyStore) Save(payload []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.payload = append([]byte(nil), payload...)
	return nil
}

func (store *memoryKeyStore) Create(payload []byte) ([]byte, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.payload != nil {
		return append([]byte(nil), store.payload...), false, nil
	}
	store.payload = append([]byte(nil), payload...)
	return append([]byte(nil), payload...), true, nil
}

func (store *memoryKeyStore) Delete() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.payload == nil {
		return keystore.ErrNotFound
	}
	clear(store.payload)
	store.payload = nil
	return nil
}

func integerString(value int) string {
	var encoded [20]byte
	index := len(encoded)
	for value > 0 {
		index--
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded[index:])
}

func boolPointer(value bool) *bool {
	return &value
}
