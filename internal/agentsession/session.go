// Package agentsession provides the shared authenticated-agent session
// middleware used by commands that require an autonomous agent principal.
package agentsession

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/keystore"
)

// TokenClient is the narrow authentication boundary required by Middleware.
type TokenClient interface {
	AcquireToken(
		context.Context,
		keystore.Store,
		agentauth.Request,
	) (agentauth.SessionToken, error)
}

// Middleware guarantees that an authenticated agent session exists. Missing
// agent credentials are enrolled autonomously through CreateIfMissing.
type Middleware struct {
	client   TokenClient
	store    keystore.Store
	register func() error

	mu    sync.Mutex
	token agentauth.SessionToken
}

// New constructs shared agent-session middleware. register persists safe,
// non-secret identity metadata after authentication succeeds.
func New(
	client TokenClient,
	store keystore.Store,
	register func() error,
) (*Middleware, error) {
	if client == nil || store == nil || register == nil {
		return nil, errors.New("invalid agent session middleware")
	}
	return &Middleware{client: client, store: store, register: register}, nil
}

// Ensure returns a usable in-memory session, autonomously creating and
// authenticating the agent account when no credential is installed.
func (middleware *Middleware) Ensure(
	ctx context.Context,
	refresh bool,
) (agentauth.SessionToken, error) {
	middleware.mu.Lock()
	defer middleware.mu.Unlock()

	if !refresh &&
		middleware.token.AuthorizationHeader() != "" &&
		time.Until(middleware.token.AccessExpiresAt) > 30*time.Second {
		return middleware.token, nil
	}
	token, err := middleware.client.AcquireToken(
		ctx,
		middleware.store,
		agentauth.Request{Mode: agentauth.CreateIfMissing},
	)
	if err != nil {
		return agentauth.SessionToken{}, err
	}
	if err := middleware.register(); err != nil {
		return agentauth.SessionToken{}, err
	}
	middleware.token = token
	return middleware.token, nil
}

// Authorization implements searchclient.AuthorizationSource through the same
// session middleware used by every other authenticated command.
func (middleware *Middleware) Authorization(
	ctx context.Context,
	refresh bool,
) (string, error) {
	token, err := middleware.Ensure(ctx, refresh)
	if err != nil {
		return "", err
	}
	return token.AuthorizationHeader(), nil
}
