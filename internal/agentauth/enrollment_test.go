package agentauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/kado-so/search/internal/agentkey"
	"github.com/kado-so/search/internal/keystore"
)

func TestPinnedPhase02BDiscoveryFixture(t *testing.T) {
	t.Parallel()

	encoded, err := os.ReadFile("testdata/discovery.v0.1.json")
	if err != nil {
		t.Fatalf("ReadFile(discovery fixture) error = %v", err)
	}
	digest := sha256.Sum256(encoded)
	if got := rawBase64URL.EncodeToString(digest[:]); got !=
		"idrw2TAbw9T_-ii2Mjkttjtrbzq1uabr6CPjF68nLlo" {
		t.Fatalf("discovery fixture checksum changed: %q", got)
	}
	var fixture struct {
		Authorization authorizationMetadata     `json:"authorization_server"`
		Protected     protectedResourceMetadata `json:"protected_resource"`
		Extension     agentProtocolMetadata     `json:"agent_extension"`
	}
	if err := decodeStrictJSON(encoded, &fixture, true); err != nil {
		t.Fatalf("decodeStrictJSON(discovery fixture) error = %v", err)
	}
	if fixture.Authorization.EnrollmentEndpoint != "https://kado.so/api/auth/agent/enroll" ||
		fixture.Protected.Resource != "https://kado.so" ||
		fixture.Extension.Issuer != fixture.Authorization.Issuer {
		t.Fatalf("unexpected pinned discovery fixture: %#v", fixture)
	}
}

func TestCreateThenAuthenticateResolvesStablePrincipalAcrossRestarts(t *testing.T) {
	t.Parallel()

	serverState := newFakePersistentState()
	server := newFakeAuthServer(serverState)
	defer server.close()
	store := &memoryKeyStore{}
	first, err := newTestClient(t, server).AuthenticateOrEnroll(
		context.Background(),
		store,
		Request{Mode: CreateIfMissing},
	)
	if err != nil {
		t.Fatalf("create AuthenticateOrEnroll() error = %v", err)
	}
	if !first.Created || first.PrincipalID == "" || first.CredentialID == "" {
		t.Fatalf("create result = %#v", first)
	}

	server.restart()
	second, err := newTestClient(t, server).AuthenticateOrEnroll(
		context.Background(),
		store,
		Request{Mode: AuthenticateOnly},
	)
	if err != nil {
		t.Fatalf("authenticate AuthenticateOrEnroll() error = %v", err)
	}
	if second.Created ||
		second.PrincipalID != first.PrincipalID ||
		second.CredentialID != first.CredentialID ||
		second.ClientID != first.ClientID {
		t.Fatalf("authenticate result = %#v; create = %#v", second, first)
	}
}

func TestConcurrentCreateRetainsOneManagementIdentity(t *testing.T) {
	t.Parallel()

	server := newFakeAuthServer(newFakePersistentState())
	defer server.close()
	store := &memoryKeyStore{}
	const workers = 8
	results := make(chan Result, workers)
	failures := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			client := newTestClient(t, server)
			client.random = rand.Reader
			result, err := client.AuthenticateOrEnroll(
				context.Background(),
				store,
				Request{Mode: CreateIfMissing},
			)
			if err != nil {
				failures <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatalf("AuthenticateOrEnroll(concurrent) error = %v", err)
	}
	var expected Result
	created := 0
	count := 0
	for result := range results {
		if count == 0 {
			expected = result
		} else if result.PrincipalID != expected.PrincipalID ||
			result.CredentialID != expected.CredentialID ||
			result.ClientID != expected.ClientID {
			t.Fatalf("concurrent result = %#v, expected identity %#v", result, expected)
		}
		if result.Created {
			created++
		}
		count++
	}
	if count != workers || created != 1 {
		t.Fatalf("results=%d created=%d, want results=%d created=1", count, created, workers)
	}
}

func TestEnrollmentModesAndAdmissionAreExplicit(t *testing.T) {
	t.Parallel()

	server := newFakeAuthServer(newFakePersistentState())
	defer server.close()
	client := newTestClient(t, server)
	if _, err := client.AuthenticateOrEnroll(
		context.Background(),
		&memoryKeyStore{},
		Request{Mode: AuthenticateOnly},
	); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("authenticate-only without local key error = %v", err)
	}

	unknownStore := &memoryKeyStore{}
	signer, err := agentkey.GenerateManagementSigner()
	if err != nil {
		t.Fatalf("GenerateManagementSigner() error = %v", err)
	}
	if err := agentkey.SaveManagementSigner(unknownStore, signer); err != nil {
		t.Fatalf("SaveManagementSigner() error = %v", err)
	}
	if _, err := client.AuthenticateOrEnroll(
		context.Background(),
		unknownStore,
		Request{Mode: AuthenticateOnly},
	); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("authenticate-only unknown server key error = %v", err)
	}

	server.admissionRequired = true
	if _, err := client.AuthenticateOrEnroll(
		context.Background(),
		&memoryKeyStore{},
		Request{Mode: CreateIfMissing},
	); !errors.Is(err, ErrAdmissionRequired) {
		t.Fatalf("admission-required error = %v", err)
	}
}

func TestNonceReplayAndTamperedResultAreRejected(t *testing.T) {
	t.Parallel()

	t.Run("nonce replay", func(t *testing.T) {
		server := newFakeAuthServer(newFakePersistentState())
		defer server.close()
		server.reuseNonce = true
		store := &memoryKeyStore{}
		client := newTestClient(t, server)
		if _, err := client.AuthenticateOrEnroll(
			context.Background(),
			store,
			Request{Mode: CreateIfMissing},
		); err != nil {
			t.Fatalf("first AuthenticateOrEnroll() error = %v", err)
		}
		if _, err := client.AuthenticateOrEnroll(
			context.Background(),
			store,
			Request{Mode: AuthenticateOnly},
		); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("reused nonce error = %v", err)
		}
	})

	t.Run("token endpoint", func(t *testing.T) {
		server := newFakeAuthServer(newFakePersistentState())
		defer server.close()
		server.resultTokenURL = server.issuer() + "/other-token"
		if _, err := newTestClient(t, server).AuthenticateOrEnroll(
			context.Background(),
			&memoryKeyStore{},
			Request{Mode: CreateIfMissing},
		); !errors.Is(err, ErrProtocol) {
			t.Fatalf("tampered result error = %v", err)
		}
	})

	t.Run("status and created coupling", func(t *testing.T) {
		server := newFakeAuthServer(newFakePersistentState())
		defer server.close()
		server.resultStatus = http.StatusOK
		if _, err := newTestClient(t, server).AuthenticateOrEnroll(
			context.Background(),
			&memoryKeyStore{},
			Request{Mode: CreateIfMissing},
		); !errors.Is(err, ErrProtocol) {
			t.Fatalf("mismatched success status error = %v", err)
		}
	})
}

func TestDiscoveryRejectsRedirectIssuerEndpointAndExtensionMismatch(t *testing.T) {
	t.Parallel()

	t.Run("redirect", func(t *testing.T) {
		target := newFakeAuthServer(newFakePersistentState())
		defer target.close()
		redirect := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			http.Redirect(response, request, target.issuer(), http.StatusFound)
		}))
		defer redirect.Close()
		resource, err := url.Parse(redirect.URL)
		if err != nil {
			t.Fatalf("Parse(redirect URL) error = %v", err)
		}
		client, err := NewClient(
			resource,
			redirect.Client(),
			DefaultLimits(),
			bytes.NewReader(bytes.Repeat([]byte{0x75}, 256)),
		)
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		if _, err := client.Discover(context.Background()); !errors.Is(err, ErrRedirect) {
			t.Fatalf("Discover(redirect) error = %v", err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*fakeAuthServer)
	}{
		{
			name: "issuer",
			mutate: func(server *fakeAuthServer) {
				server.issuerValue = server.issuer() + "/wrong"
			},
		},
		{
			name: "external endpoint",
			mutate: func(server *fakeAuthServer) {
				server.nonceURL = "https://metadata-attacker.example/nonce"
			},
		},
		{
			name: "extension mismatch",
			mutate: func(server *fakeAuthServer) {
				server.extensionMismatch = true
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeAuthServer(newFakePersistentState())
			defer server.close()
			test.mutate(server)
			if _, err := newTestClient(t, server).Discover(
				context.Background(),
			); !errors.Is(err, ErrDiscovery) {
				t.Fatalf("Discover() error = %v", err)
			}
		})
	}
}

func TestProtocolObjectsRequireExactCaseSensitiveNonNullFields(t *testing.T) {
	t.Parallel()

	protected, err := json.Marshal(protectedResourceMetadata{
		Resource:                  "https://kado.so",
		AuthorizationServers:      []string{"https://kado.so"},
		ScopesSupported:           []string{"search:read"},
		BearerMethodsSupported:    []string{"header"},
		AgentPrincipalMetadataURI: "https://kado.so/.well-known/agent-principal",
	})
	if err != nil {
		t.Fatalf("Marshal(protected metadata) error = %v", err)
	}
	success := []byte(`{"status":"active","created":false,"principal_id":"agt_1","credential_id":"acred_1","client_id":"clt_1","token_endpoint":"https://kado.so/oauth/token","token_endpoint_auth_method":"private_key_jwt"}`)
	validError := []byte(`{"error":"agent_not_found","error_description":"Not enrolled.","retryable":false}`)

	tests := []struct {
		name        string
		encoded     []byte
		destination any
		fields      []string
	}{
		{
			name:        "discovery case variant",
			encoded:     bytes.Replace(protected, []byte(`"resource"`), []byte(`"Resource"`), 1),
			destination: &protectedResourceMetadata{},
			fields:      protectedResourceMetadataFields,
		},
		{
			name:        "discovery null",
			encoded:     bytes.Replace(protected, []byte(`"resource":"https://kado.so"`), []byte(`"resource":null`), 1),
			destination: &protectedResourceMetadata{},
			fields:      protectedResourceMetadataFields,
		},
		{
			name:        "success missing created",
			encoded:     bytes.Replace(success, []byte(`"created":false,`), nil, 1),
			destination: &successResponse{},
			fields:      successResponseFields,
		},
		{
			name:        "success null created",
			encoded:     bytes.Replace(success, []byte(`"created":false`), []byte(`"created":null`), 1),
			destination: &successResponse{},
			fields:      successResponseFields,
		},
		{
			name:        "success semantic duplicate",
			encoded:     bytes.Replace(success, []byte(`"created":false`), []byte(`"created":true,"CREATED":false`), 1),
			destination: &successResponse{},
			fields:      successResponseFields,
		},
		{
			name:        "error extra",
			encoded:     bytes.Replace(validError, []byte(`}`), []byte(`,"detail":"unsafe"}`), 1),
			destination: &errorResponse{},
			fields:      errorResponseFields,
		},
		{
			name:        "error null retryable",
			encoded:     bytes.Replace(validError, []byte(`"retryable":false`), []byte(`"retryable":null`), 1),
			destination: &errorResponse{},
			fields:      errorResponseFields,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := decodeExactJSONObject(
				test.encoded,
				test.destination,
				test.fields,
			); err == nil {
				t.Fatalf("decodeExactJSONObject(%s) succeeded", test.encoded)
			}
		})
	}

	var decoded successResponse
	if err := decodeExactJSONObject(success, &decoded, successResponseFields); err != nil {
		t.Fatalf("decodeExactJSONObject(valid success) error = %v", err)
	}
	if err := classifyEnrollmentFailure(http.StatusNotFound, validError); !errors.Is(
		err,
		ErrAgentNotFound,
	) {
		t.Fatalf("classifyEnrollmentFailure(valid) error = %v", err)
	}
}

func newTestClient(t *testing.T, server *fakeAuthServer) *Client {
	t.Helper()
	base, err := url.Parse(server.issuer())
	if err != nil {
		t.Fatalf("Parse(server URL) error = %v", err)
	}
	client, err := NewClient(
		base,
		server.server.Client(),
		DefaultLimits(),
		bytes.NewReader(deterministicBytes(4096)),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

var _ keystore.Store = (*memoryKeyStore)(nil)

func deterministicBytes(size int) []byte {
	value := make([]byte, size)
	for index := range value {
		value[index] = byte(index)
	}
	return value
}
