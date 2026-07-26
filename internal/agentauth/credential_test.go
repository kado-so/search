package agentauth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/kado-so/search/internal/keystore"
)

func TestCredentialStatusAndRevocationLifecycle(t *testing.T) {
	t.Parallel()

	server := newFakeAuthServer(newFakePersistentState())
	defer server.close()
	client := newTestClient(t, server)
	store := &memoryKeyStore{}
	enrolled, err := client.AuthenticateOrEnroll(
		context.Background(),
		store,
		Request{Mode: CreateIfMissing},
	)
	if err != nil {
		t.Fatalf("AuthenticateOrEnroll() error = %v", err)
	}
	status, err := client.CredentialStatus(context.Background(), store)
	if err != nil {
		t.Fatalf("CredentialStatus() error = %v", err)
	}
	if status != (CredentialStatus{
		Status:       StatusActive,
		PrincipalID:  enrolled.PrincipalID,
		CredentialID: enrolled.CredentialID,
		ClientID:     enrolled.ClientID,
	}) {
		t.Fatalf("CredentialStatus() = %#v", status)
	}

	keyMaterial, err := store.Load()
	if err != nil {
		t.Fatalf("Load() before revoke error = %v", err)
	}
	revoked, err := client.RevokeCurrentCredential(context.Background(), store)
	if err != nil {
		t.Fatalf("RevokeCurrentCredential() error = %v", err)
	}
	if revoked.Status != StatusRevoked ||
		revoked.PrincipalID != enrolled.PrincipalID ||
		revoked.CredentialID != enrolled.CredentialID ||
		revoked.ClientID != enrolled.ClientID {
		t.Fatalf("RevokeCurrentCredential() = %#v", revoked)
	}
	if _, err := store.Load(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("Load() after revoke error = %v, want ErrNotFound", err)
	}
	if _, err := client.AuthenticateOrEnroll(
		context.Background(),
		store,
		Request{Mode: AuthenticateOnly},
	); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("later AuthenticateOnly error = %v, want ErrCredentialNotFound", err)
	}

	// Restoring the now-revoked key demonstrates that server authorization, not
	// only local deletion, rejects later authentication.
	if err := store.Save(keyMaterial); err != nil {
		t.Fatalf("Save(revoked key) error = %v", err)
	}
	clear(keyMaterial)
	if _, err := client.AuthenticateOrEnroll(
		context.Background(),
		store,
		Request{Mode: AuthenticateOnly},
	); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf(
			"AuthenticateOnly() with restored revoked credential error = %v, want ErrCredentialRevoked",
			err,
		)
	}
}

func TestCredentialRevocationIsLocallyIdempotent(t *testing.T) {
	t.Parallel()

	server := newFakeAuthServer(newFakePersistentState())
	defer server.close()
	client := newTestClient(t, server)
	store := &memoryKeyStore{}
	if _, err := client.AuthenticateOrEnroll(
		context.Background(),
		store,
		Request{Mode: CreateIfMissing},
	); err != nil {
		t.Fatalf("AuthenticateOrEnroll() error = %v", err)
	}
	if _, err := client.RevokeCurrentCredential(context.Background(), store); err != nil {
		t.Fatalf("first RevokeCurrentCredential() error = %v", err)
	}
	server.mu.Lock()
	callsAfterFirst := server.credentialCalls
	server.mu.Unlock()

	status, err := client.RevokeCurrentCredential(context.Background(), store)
	if err != nil {
		t.Fatalf("second RevokeCurrentCredential() error = %v", err)
	}
	if status != (CredentialStatus{Status: StatusNotConfigured}) {
		t.Fatalf("second RevokeCurrentCredential() = %#v", status)
	}
	server.mu.Lock()
	callsAfterSecond := server.credentialCalls
	server.mu.Unlock()
	if callsAfterSecond != callsAfterFirst {
		t.Fatalf("second local no-op made server call: %d -> %d", callsAfterFirst, callsAfterSecond)
	}
}

func TestCredentialRevocationRetriesAfterLocalDeleteFailure(t *testing.T) {
	t.Parallel()

	server := newFakeAuthServer(newFakePersistentState())
	defer server.close()
	client := newTestClient(t, server)
	store := &deleteFailureStore{Store: &memoryKeyStore{}, failures: 1}
	if _, err := client.AuthenticateOrEnroll(
		context.Background(),
		store,
		Request{Mode: CreateIfMissing},
	); err != nil {
		t.Fatalf("AuthenticateOrEnroll() error = %v", err)
	}
	if _, err := client.RevokeCurrentCredential(
		context.Background(),
		store,
	); !errors.Is(err, ErrCredentialCleanup) {
		t.Fatalf("first RevokeCurrentCredential() error = %v, want ErrCredentialCleanup", err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("key removed after failed local delete: %v", err)
	}
	inspected, err := client.CredentialStatus(context.Background(), store)
	if err != nil {
		t.Fatalf("CredentialStatus() after server revocation error = %v", err)
	}
	if inspected.Status != StatusRevoked {
		t.Fatalf("CredentialStatus() after server revocation = %#v", inspected)
	}
	status, err := client.RevokeCurrentCredential(context.Background(), store)
	if err != nil {
		t.Fatalf("retry RevokeCurrentCredential() error = %v", err)
	}
	if status.Status != StatusRevoked {
		t.Fatalf("retry status = %#v", status)
	}
	if _, err := store.Load(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("key retained after successful retry: %v", err)
	}
}

func TestCredentialRevocationNeverDeletesConcurrentReenrollment(t *testing.T) {
	t.Parallel()

	server := newFakeAuthServer(newFakePersistentState())
	defer server.close()
	client := newTestClient(t, server)
	underlying := &memoryKeyStore{}
	first, err := client.AuthenticateOrEnroll(
		context.Background(),
		underlying,
		Request{Mode: CreateIfMissing},
	)
	if err != nil {
		t.Fatalf("AuthenticateOrEnroll(first) error = %v", err)
	}
	blocking := &blockingConditionalDeleteStore{
		Store:   underlying,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	revokeResult := make(chan error, 1)
	go func() {
		_, revokeErr := client.RevokeCurrentCredential(context.Background(), blocking)
		revokeResult <- revokeErr
	}()
	<-blocking.entered

	if err := underlying.Delete(); err != nil {
		t.Fatalf("Delete(old local key) error = %v", err)
	}
	second, err := client.AuthenticateOrEnroll(
		context.Background(),
		underlying,
		Request{Mode: CreateIfMissing},
	)
	if err != nil {
		t.Fatalf("AuthenticateOrEnroll(replacement) error = %v", err)
	}
	if second.PrincipalID == first.PrincipalID {
		t.Fatalf("replacement reused revoked principal: %#v", second)
	}
	replacement, err := underlying.Load()
	if err != nil {
		t.Fatalf("Load(replacement) error = %v", err)
	}
	close(blocking.release)
	if err := <-revokeResult; !errors.Is(err, ErrCredentialChanged) {
		t.Fatalf("RevokeCurrentCredential() error = %v, want ErrCredentialChanged", err)
	}
	current, err := underlying.Load()
	if err != nil {
		t.Fatalf("Load(after stale cleanup) error = %v", err)
	}
	if string(current) != string(replacement) {
		t.Fatal("stale revocation deleted or changed the replacement key")
	}
	clear(replacement)
	clear(current)
	if _, err := client.AuthenticateOrEnroll(
		context.Background(),
		underlying,
		Request{Mode: AuthenticateOnly},
	); err != nil {
		t.Fatalf("replacement credential no longer authenticates: %v", err)
	}
}

func TestCredentialRevocationRetainsKeyOnServerOrProtocolFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		serverStatus    int
		invalidResponse bool
	}{
		{name: "server failure", serverStatus: http.StatusServiceUnavailable},
		{name: "unexpected response field", invalidResponse: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := newFakeAuthServer(newFakePersistentState())
			defer server.close()
			client := newTestClient(t, server)
			store := &memoryKeyStore{}
			if _, err := client.AuthenticateOrEnroll(
				context.Background(),
				store,
				Request{Mode: CreateIfMissing},
			); err != nil {
				t.Fatalf("AuthenticateOrEnroll() error = %v", err)
			}
			server.mu.Lock()
			server.credentialStatus = test.serverStatus
			server.credentialInvalid = test.invalidResponse
			server.mu.Unlock()
			if _, err := client.RevokeCurrentCredential(context.Background(), store); err == nil {
				t.Fatal("RevokeCurrentCredential() succeeded")
			}
			if _, err := store.Load(); err != nil {
				t.Fatalf("key removed after unconfirmed revocation: %v", err)
			}
		})
	}
}

func TestCredentialStatusDoesNotCreateMissingKey(t *testing.T) {
	t.Parallel()

	server := newFakeAuthServer(newFakePersistentState())
	defer server.close()
	client := newTestClient(t, server)
	store := &memoryKeyStore{}
	if _, err := client.CredentialStatus(
		context.Background(),
		store,
	); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("CredentialStatus() error = %v, want ErrCredentialNotFound", err)
	}
	if _, err := store.Load(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("CredentialStatus() created key: %v", err)
	}
	server.mu.Lock()
	calls := server.credentialCalls
	server.mu.Unlock()
	if calls != 0 {
		t.Fatalf("missing-key status made %d server calls", calls)
	}
}

type deleteFailureStore struct {
	keystore.Store
	mu       sync.Mutex
	failures int
}

type blockingConditionalDeleteStore struct {
	keystore.Store
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (store *blockingConditionalDeleteStore) DeleteIfMatches(
	expected []byte,
) (bool, error) {
	store.once.Do(func() { close(store.entered) })
	<-store.release
	return store.Store.DeleteIfMatches(expected)
}

func (store *deleteFailureStore) Delete() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failures > 0 {
		store.failures--
		return keystore.ErrUnavailable
	}
	return store.Store.Delete()
}

func (store *deleteFailureStore) DeleteIfMatches(expected []byte) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failures > 0 {
		store.failures--
		return false, keystore.ErrUnavailable
	}
	return store.Store.DeleteIfMatches(expected)
}
