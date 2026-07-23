// Package cli owns command parsing and ordinary terminal output for kado.
package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/config"
	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/keystore"
)

const helpText = `Kado Search command-line client

Usage:
  kado <command>

Commands:
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

type dependencies struct {
	newAuth func() (authCommands, error)
}

type defaultAuthCommands struct {
	client *agentauth.Client
	store  keystore.Store
}

var outputIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Run executes one CLI invocation and returns a process exit status.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return runWithDependencies(args, stdout, stderr, info, dependencies{
		newAuth: newDefaultAuthCommands,
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
	default:
		return usageError("unknown command; run 'kado help' for usage")
	}
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

func usageError(message string) error {
	return diagnostic.New("invalid_usage", message, diagnostic.ExitUsage, nil)
}
