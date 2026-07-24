// Package cli owns command parsing and ordinary terminal output for kado.
package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"

	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/config"
	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/keystore"
	"github.com/kado-so/search/internal/searchclient"
)

const helpText = `Kado Search command-line client

Usage:
  kado <command>

Commands:
  search <query>  Run an authenticated Search to completion
  auth status    Show safe current-installation identity state
  auth revoke    Revoke the current installation
  help           Show this help
  version        Show bounded build information

Options:
  -h, --help       Show this help
  -v, --version    Show bounded build information
`

type authCommands interface {
	Status(context.Context) (agentauth.CredentialStatus, error)
	Revoke(context.Context) (agentauth.CredentialStatus, error)
}

type searchCommands interface {
	Run(context.Context, string, searchclient.RunOptions) (searchclient.Result, error)
}

type dependencies struct {
	newAuth   func() (authCommands, error)
	newSearch func() (searchCommands, error)
}

type defaultAuthCommands struct {
	client *agentauth.Client
	store  keystore.Store
}

type defaultSearchCommands struct {
	client *searchclient.Client
}

type phase02CAuthorizationSource struct {
	client *agentauth.Client
	store  keystore.Store
	token  agentauth.SessionToken
}

var outputIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)

// Run executes one CLI invocation and returns a process exit status.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return runWithDependencies(args, stdout, stderr, info, dependencies{
		newAuth:   newDefaultAuthCommands,
		newSearch: newDefaultSearchCommands,
	})
}

func runWithDependencies(
	args []string,
	stdout,
	stderr io.Writer,
	info buildinfo.Info,
	dependencies dependencies,
) int {
	err := run(args, stdout, info, dependencies)
	if err == nil {
		return 0
	}
	code, message, exitCode := diagnostic.Public(err)
	_, _ = fmt.Fprintf(stderr, "kado: %s [%s]\n", message, code)
	return exitCode
}

func run(
	args []string,
	stdout io.Writer,
	info buildinfo.Info,
	dependencies dependencies,
) error {
	if len(args) == 0 {
		_, _ = io.WriteString(stdout, helpText)
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return usageError("help does not accept arguments")
		}
		_, _ = io.WriteString(stdout, helpText)
		return nil
	case "version", "-v", "--version":
		if len(args) != 1 {
			return usageError("version does not accept arguments")
		}
		_, _ = fmt.Fprintln(stdout, info.Line())
		return nil
	case "auth":
		return runAuth(args[1:], stdout, dependencies)
	case "search":
		return runSearch(args[1:], stdout, dependencies)
	default:
		return usageError("unknown command; run 'kado help' for usage")
	}
}

func runSearch(args []string, stdout io.Writer, dependencies dependencies) error {
	query, options, err := parseSearchArguments(args)
	if err != nil {
		return err
	}
	if dependencies.newSearch == nil {
		return searchDiagnostic(errors.New("Search unavailable"))
	}
	search, err := dependencies.newSearch()
	if err != nil {
		return searchDiagnostic(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, err := search.Run(ctx, query, options)
	if err != nil {
		return searchDiagnostic(err)
	}
	if result.Document.Status != searchclient.StatusComplete ||
		!outputIdentifierPattern.MatchString(result.Document.SearchID) ||
		len(result.Pages) < 1 {
		return searchDiagnostic(searchclient.ErrProtocol)
	}
	_, _ = fmt.Fprintf(
		stdout,
		"status: complete\nsearch: %s\npages: %d\n",
		result.Document.SearchID,
		len(result.Pages),
	)
	return nil
}

func parseSearchArguments(args []string) (string, searchclient.RunOptions, error) {
	options := searchclient.DefaultRunOptions()
	var queryParts []string
	var answer string
	answerSet := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--":
			queryParts = append(queryParts, args[index+1:]...)
			index = len(args)
		case "--timeout":
			index++
			if index >= len(args) {
				return "", options, usageError("search --timeout requires a duration")
			}
			timeout, err := time.ParseDuration(args[index])
			if err != nil || timeout <= 0 || timeout > 30*time.Minute {
				return "", options, usageError("search timeout must be between 1ns and 30m")
			}
			options.Timeout = timeout
		case "--answer":
			index++
			if index >= len(args) || answerSet {
				return "", options, usageError("search --answer requires one value")
			}
			answer = args[index]
			answerSet = true
		case "--first-page":
			options.FollowPages = false
		case "--retry":
			options.RetryFailure = true
		default:
			if strings.HasPrefix(argument, "-") {
				return "", options, usageError("unknown search option")
			}
			queryParts = append(queryParts, argument)
		}
	}
	query := strings.Join(queryParts, " ")
	if query == "" {
		return "", options, usageError(
			"usage: kado search [--timeout duration] [--answer value] [--first-page] [--retry] <query>",
		)
	}
	if answerSet {
		options.Clarify = func(context.Context, searchclient.Question) (string, error) {
			return answer, nil
		}
	}
	return query, options, nil
}

func runAuth(args []string, stdout io.Writer, dependencies dependencies) error {
	if len(args) != 1 {
		return usageError("usage: kado auth <status|revoke>")
	}
	if args[0] != "status" && args[0] != "revoke" {
		return usageError("unknown auth command; use 'status' or 'revoke'")
	}
	if dependencies.newAuth == nil {
		return authDiagnostic(args[0], errors.New("authentication unavailable"))
	}
	auth, err := dependencies.newAuth()
	if err != nil {
		return authDiagnostic(args[0], err)
	}
	var status agentauth.CredentialStatus
	if args[0] == "status" {
		status, err = auth.Status(context.Background())
		if errors.Is(err, agentauth.ErrCredentialNotFound) {
			status = agentauth.CredentialStatus{Status: agentauth.StatusNotConfigured}
			err = nil
		}
	} else {
		status, err = auth.Revoke(context.Background())
	}
	if err != nil {
		return authDiagnostic(args[0], err)
	}
	return renderCredentialStatus(stdout, status)
}

func renderCredentialStatus(stdout io.Writer, status agentauth.CredentialStatus) error {
	switch status.Status {
	case agentauth.StatusNotConfigured:
		if status.PrincipalID != "" || status.CredentialID != "" || status.ClientID != "" {
			return authDiagnostic("status", agentauth.ErrProtocol)
		}
		_, _ = fmt.Fprintln(stdout, "status: not-configured")
		return nil
	case agentauth.StatusActive, agentauth.StatusRevoked:
		if !outputIdentifierPattern.MatchString(status.PrincipalID) ||
			!outputIdentifierPattern.MatchString(status.CredentialID) ||
			!outputIdentifierPattern.MatchString(status.ClientID) {
			return authDiagnostic("status", agentauth.ErrProtocol)
		}
		_, _ = fmt.Fprintf(
			stdout,
			"status: %s\nprincipal: %s\ncredential: %s\nclient: %s\n",
			status.Status,
			status.PrincipalID,
			status.CredentialID,
			status.ClientID,
		)
		return nil
	default:
		return authDiagnostic("status", agentauth.ErrProtocol)
	}
}

func authDiagnostic(operation string, cause error) error {
	if operation == "revoke" && errors.Is(cause, agentauth.ErrCredentialChanged) {
		return diagnostic.New(
			"auth_credential_changed",
			"the prior installation was revoked, but the local credential changed; retry to revoke the current installation",
			diagnostic.ExitFailure,
			cause,
		)
	}
	if operation == "revoke" && errors.Is(cause, agentauth.ErrCredentialCleanup) {
		return diagnostic.New(
			"auth_local_cleanup_failed",
			"the installation was revoked, but its local credential could not be removed",
			diagnostic.ExitFailure,
			cause,
		)
	}
	if operation == "revoke" {
		return diagnostic.New(
			"auth_revoke_failed",
			"could not revoke the current installation",
			diagnostic.ExitFailure,
			cause,
		)
	}
	return diagnostic.New(
		"auth_status_failed",
		"could not read current installation authentication status",
		diagnostic.ExitFailure,
		cause,
	)
}

func searchDiagnostic(cause error) error {
	var needsInput *searchclient.NeedsInputError
	if errors.As(cause, &needsInput) {
		return diagnostic.New(
			"search_needs_input",
			"Search requires clarification; rerun with --answer <value>",
			diagnostic.ExitFailure,
			cause,
		)
	}
	var failure *searchclient.FailureError
	if errors.As(cause, &failure) {
		return diagnostic.New(
			failure.Failure.Code,
			failure.Failure.Message,
			diagnostic.ExitFailure,
			cause,
		)
	}
	if errors.Is(cause, searchclient.ErrCanceled) {
		return diagnostic.New(
			"search_canceled",
			"Search was canceled",
			diagnostic.ExitFailure,
			cause,
		)
	}
	if errors.Is(cause, context.Canceled) {
		return diagnostic.New(
			"search_interrupted",
			"Search was interrupted and cancellation was requested",
			diagnostic.ExitFailure,
			cause,
		)
	}
	if errors.Is(cause, searchclient.ErrTimeout) {
		return diagnostic.New(
			"search_timeout",
			"Search did not complete before the timeout",
			diagnostic.ExitFailure,
			cause,
		)
	}
	var remote *searchclient.Error
	if errors.As(cause, &remote) {
		return diagnostic.New(
			remote.Code(),
			remote.Error(),
			diagnostic.ExitFailure,
			cause,
		)
	}
	return diagnostic.New(
		"search_failed",
		"Could not complete the Search",
		diagnostic.ExitFailure,
		cause,
	)
}

func newDefaultAuthCommands() (authCommands, error) {
	safeConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	client, err := agentauth.NewClient(
		safeConfig.BaseURL,
		&http.Client{Timeout: 30 * time.Second},
		agentauth.DefaultLimits(),
		rand.Reader,
	)
	if err != nil {
		return nil, err
	}
	return &defaultAuthCommands{
		client: client,
		store:  keystore.NewOSKeychainStore(),
	}, nil
}

func newDefaultSearchCommands() (searchCommands, error) {
	safeConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	authClient, err := agentauth.NewClient(
		safeConfig.BaseURL,
		httpClient,
		agentauth.DefaultLimits(),
		rand.Reader,
	)
	if err != nil {
		return nil, err
	}
	authorization := &phase02CAuthorizationSource{
		client: authClient,
		store:  keystore.NewOSKeychainStore(),
	}
	search, err := searchclient.New(
		safeConfig.BaseURL,
		httpClient,
		authorization,
		searchclient.DefaultLimits(),
	)
	if err != nil {
		return nil, err
	}
	return &defaultSearchCommands{client: search}, nil
}

func (commands *defaultAuthCommands) Status(
	ctx context.Context,
) (agentauth.CredentialStatus, error) {
	return commands.client.CredentialStatus(ctx, commands.store)
}

func (commands *defaultAuthCommands) Revoke(
	ctx context.Context,
) (agentauth.CredentialStatus, error) {
	return commands.client.RevokeCurrentCredential(ctx, commands.store)
}

func (commands *defaultSearchCommands) Run(
	ctx context.Context,
	query string,
	options searchclient.RunOptions,
) (searchclient.Result, error) {
	return commands.client.Run(ctx, query, options)
}

func (source *phase02CAuthorizationSource) Authorization(
	ctx context.Context,
	refresh bool,
) (string, error) {
	if !refresh &&
		source.token.AuthorizationHeader() != "" &&
		time.Until(source.token.AccessExpiresAt) > 30*time.Second {
		return source.token.AuthorizationHeader(), nil
	}
	token, err := source.client.AcquireToken(
		ctx,
		source.store,
		agentauth.Request{Mode: agentauth.CreateIfMissing},
	)
	if err != nil {
		return "", err
	}
	source.token = token
	return source.token.AuthorizationHeader(), nil
}

func usageError(message string) error {
	return diagnostic.New("invalid_usage", message, diagnostic.ExitUsage, nil)
}
