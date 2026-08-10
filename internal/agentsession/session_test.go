package agentsession

import (
	"context"
	"errors"
	"testing"

	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/keystore"
)

func TestEnsureUsesCreateIfMissingAndRegistersAuthenticatedIdentity(t *testing.T) {
	t.Parallel()
	store := &testStore{}
	client := &fakeTokenClient{token: agentauth.SessionToken{
		PrincipalID: "agt_123", CredentialID: "acred_456", ClientID: "clt_789",
	}}
	registrations := 0
	middleware, err := New(client, store, func() error {
		registrations++
		return nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	token, err := middleware.Ensure(context.Background(), false)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if token.PrincipalID != "agt_123" || client.calls != 1 ||
		client.request.Mode != agentauth.CreateIfMissing || registrations != 1 {
		t.Fatalf(
			"token=%v calls=%d request=%v registrations=%d",
			token, client.calls, client.request, registrations,
		)
	}
}

func TestEnsureDoesNotRegisterFailedAuthentication(t *testing.T) {
	t.Parallel()
	store := &testStore{}
	client := &fakeTokenClient{err: agentauth.ErrAuthentication}
	registrations := 0
	middleware, err := New(client, store, func() error {
		registrations++
		return nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := middleware.Ensure(context.Background(), false); !errors.Is(err, agentauth.ErrAuthentication) {
		t.Fatalf("Ensure() error = %v, want ErrAuthentication", err)
	}
	if registrations != 0 {
		t.Fatalf("registrations = %d, want 0", registrations)
	}
}

type testStore struct{}

func (*testStore) Load() ([]byte, error)                     { return nil, keystore.ErrNotFound }
func (*testStore) Create(value []byte) ([]byte, bool, error) { return value, true, nil }
func (*testStore) Save([]byte) error                         { return nil }
func (*testStore) Delete() error                             { return nil }
func (*testStore) DeleteIfMatches([]byte) (bool, error)      { return true, nil }

type fakeTokenClient struct {
	token   agentauth.SessionToken
	err     error
	request agentauth.Request
	calls   int
}

func (client *fakeTokenClient) AcquireToken(
	_ context.Context,
	_ keystore.Store,
	request agentauth.Request,
) (agentauth.SessionToken, error) {
	client.calls++
	client.request = request
	return client.token, client.err
}
