package protocoltelemetry

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/agentidentity"
	"github.com/kado-so/search/internal/config"
	"github.com/kado-so/search/internal/keystore"
	"github.com/kado-so/search/internal/localstate"
	"github.com/kado-so/search/internal/requestmeta"
)

const reportTimeout = 2 * time.Second

// Attempt starts one best-effort report in the background and preserves the
// immutable fields needed for its matching completion report.
type Attempt struct {
	reporter   reporter
	invocation invocation
	attemptID  string
	startedAt  time.Time
	started    chan struct{}
}

// Begin classifies a public Kado MCP/A2A invocation and starts fail-open
// reporting when an existing Kado credential is available.
func Begin(argv []string) *Attempt {
	classified, ok := classify(argv)
	if !ok {
		return nil
	}
	configured, err := defaultReporter(classified.agent)
	if err != nil {
		return nil
	}
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return nil
	}
	attempt := &Attempt{
		reporter: configured, invocation: classified,
		attemptID: base64.RawURLEncoding.EncodeToString(identifier),
		startedAt: time.Now().UTC(), started: make(chan struct{}),
	}
	go func() {
		defer close(attempt.started)
		ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
		defer cancel()
		_ = attempt.reporter.Report(ctx, eventFor(classified, attempt.attemptID, PhaseStarted, attempt.startedAt, nil))
	}()
	return attempt
}

// Finish submits the matching completion event without changing command output
// or exit status. All telemetry failures are intentionally ignored.
func (attempt *Attempt) Finish(exitCode int) {
	if attempt == nil {
		return
	}
	select {
	case <-attempt.started:
	case <-time.After(reportTimeout):
	}
	ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()
	_ = attempt.reporter.Report(ctx, eventFor(attempt.invocation, attempt.attemptID, PhaseFinished, attempt.startedAt, &exitCode))
}

type existingAuthorization struct {
	client *agentauth.Client
	store  keystore.Store

	mu    sync.Mutex
	token agentauth.SessionToken
}

func (source *existingAuthorization) Authorization(ctx context.Context, refresh bool) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if !refresh && source.token.AuthorizationHeader() != "" && time.Until(source.token.AccessExpiresAt) > 30*time.Second {
		return source.token.AuthorizationHeader(), nil
	}
	token, err := source.client.AcquireToken(ctx, source.store, agentauth.Request{Mode: agentauth.AuthenticateOnly})
	if err != nil {
		return "", err
	}
	source.token = token
	return token.AuthorizationHeader(), nil
}

func defaultReporter(override string) (reporter, error) {
	detection, err := agentidentity.Detect(override)
	if err != nil {
		return nil, err
	}
	safeConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	store, err := telemetryStore(safeConfig, detection.Agent)
	if err != nil {
		return nil, err
	}
	encoded, err := store.Load()
	if err != nil {
		return nil, err
	}
	clear(encoded)
	host, err := localstate.EnsureHost(safeConfig.ConfigDir)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 32 * 1024
	httpClient := &http.Client{
		Timeout:   reportTimeout,
		Transport: requestmeta.NewTransport(transport, detection.Agent, host.ID),
	}
	authentication, err := agentauth.NewClient(
		safeConfig.BaseURL,
		httpClient,
		agentauth.DefaultLimits(),
		rand.Reader,
	)
	if err != nil {
		return nil, err
	}
	return newClient(safeConfig.BaseURL, httpClient, &existingAuthorization{client: authentication, store: store})
}

func telemetryStore(safeConfig config.Config, agent string) (keystore.Store, error) {
	switch safeConfig.CredentialBackend {
	case config.CredentialBackendOS:
		return keystore.NewOSKeychainStore(agent)
	case config.CredentialBackendFile:
		return keystore.NewAgentFileStore(safeConfig.SecretsDir, agent)
	default:
		return nil, errors.New("unsupported credential backend")
	}
}
