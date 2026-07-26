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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/config"
	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/keystore"
	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/searchclient"
	"github.com/kado-so/search/internal/searchcontract"
	"github.com/kado-so/search/internal/searchoutput"
)

const helpText = `Kado Search command-line client

Usage:
  kado <command>

Commands:
  search <query>  Run an authenticated Search to completion
  auth status    Show safe current-installation identity state
  auth revoke    Revoke the current installation
  update         Install a verified signed CLI release
  uninstall      Remove the CLI; preserve credentials by default
  release verify Verify a downloaded release bundle
  help           Show this help
  version        Show bounded build information

Options:
  --json            Emit one exact canonical Search Document
  --jsonl           Emit deterministic result and pagination records
  --width columns   Human output width from 40 to 160 (default 96)
  -h, --help        Show this help
  -v, --version     Show bounded build information
  version --json    Show deterministic executable provenance
`

const acceptanceCredentialFileEnvironment = "KADO_ACCEPTANCE_CREDENTIAL_FILE"

type authCommands interface {
	Status(context.Context) (agentauth.CredentialStatus, error)
	Revoke(context.Context) (agentauth.CredentialStatus, error)
}

type searchCommands interface {
	Run(context.Context, string, searchclient.RunOptions) (searchRunResult, error)
}

type releaseCommands interface {
	Update(context.Context, releaseclient.Options) (releaseclient.Result, error)
	Uninstall() error
	VerifyBundle(string) (releaseclient.Metadata, releaseclient.Target, error)
}

type searchRunResult struct {
	status    string
	canonical []byte
	pages     [][]byte
}

type dependencies struct {
	newAuth    func() (authCommands, error)
	newSearch  func() (searchCommands, error)
	newRelease func(buildinfo.Info) (releaseCommands, error)
}

type defaultAuthCommands struct {
	client *agentauth.Client
	store  keystore.Store
}

type defaultSearchCommands struct {
	client *searchclient.Client
}

type defaultReleaseCommands struct {
	manager    releaseclient.Manager
	executable string
	info       buildinfo.Info
}

type phase02CAuthorizationSource struct {
	client *agentauth.Client
	store  keystore.Store
	token  agentauth.SessionToken
}

var outputIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)
var errBrokenPipe = errors.New("CLI output pipe closed")

// Run executes one CLI invocation and returns a process exit status.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return runWithDependencies(args, stdout, stderr, info, dependencies{
		newAuth:    newDefaultAuthCommands,
		newSearch:  newDefaultSearchCommands,
		newRelease: newDefaultReleaseCommands,
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
	if errors.Is(err, errBrokenPipe) {
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
		if len(args) == 2 && args[0] == "version" && args[1] == "--json" {
			encoded, err := info.JSON()
			if err != nil {
				return diagnostic.New(
					"version_failed",
					"could not render executable provenance",
					diagnostic.ExitFailure,
					err,
				)
			}
			_, _ = stdout.Write(encoded)
			return nil
		}
		if len(args) != 1 {
			return usageError("version does not accept arguments")
		}
		_, _ = fmt.Fprintln(stdout, info.Line())
		return nil
	case "auth":
		return runAuth(args[1:], stdout, dependencies)
	case "search":
		return runSearch(args[1:], stdout, dependencies)
	case "update":
		return runUpdate(args[1:], stdout, info, dependencies)
	case "uninstall":
		return runUninstall(args[1:], stdout, info, dependencies)
	case "release":
		return runRelease(args[1:], stdout, info, dependencies)
	default:
		return usageError("unknown command; run 'kado help' for usage")
	}
}

func runRelease(
	args []string,
	stdout io.Writer,
	info buildinfo.Info,
	dependencies dependencies,
) error {
	if len(args) != 3 || args[0] != "verify" || args[1] != "--directory" {
		return usageError("usage: kado release verify --directory <path>")
	}
	if dependencies.newRelease == nil {
		return releaseDiagnostic(errors.New("release support unavailable"))
	}
	releases, err := dependencies.newRelease(info)
	if err != nil {
		return releaseDiagnostic(err)
	}
	metadata, target, err := releases.VerifyBundle(args[2])
	if err != nil {
		return releaseDiagnostic(err)
	}
	_, _ = fmt.Fprintf(
		stdout,
		"verified kado %s for %s/%s\n",
		metadata.Version,
		target.OS,
		target.Arch,
	)
	return nil
}

func runUpdate(
	args []string,
	stdout io.Writer,
	info buildinfo.Info,
	dependencies dependencies,
) error {
	options := releaseclient.Options{CurrentVersion: info.Version}
	for _, argument := range args {
		switch argument {
		case "--dry-run":
			options.DryRun = true
		case "--allow-downgrade":
			options.AllowDowngrade = true
		default:
			return usageError("usage: kado update [--dry-run] [--allow-downgrade]")
		}
	}
	if dependencies.newRelease == nil {
		return releaseDiagnostic(errors.New("release support unavailable"))
	}
	releases, err := dependencies.newRelease(info)
	if err != nil {
		return releaseDiagnostic(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, err := releases.Update(ctx, options)
	if err != nil {
		return releaseDiagnostic(err)
	}
	switch {
	case result.DryRun:
		_, _ = fmt.Fprintf(
			stdout,
			"verified kado %s for %s; no files changed\n",
			result.ToVersion,
			result.Target,
		)
	case result.Changed:
		_, _ = fmt.Fprintf(
			stdout,
			"updated kado %s to %s for %s\n",
			result.FromVersion,
			result.ToVersion,
			result.Target,
		)
	default:
		_, _ = fmt.Fprintf(stdout, "kado %s is already current\n", result.ToVersion)
	}
	return nil
}

func runUninstall(
	args []string,
	stdout io.Writer,
	info buildinfo.Info,
	dependencies dependencies,
) error {
	confirmed := false
	purgeCredentials := false
	for _, argument := range args {
		switch argument {
		case "--yes":
			confirmed = true
		case "--purge-credentials":
			purgeCredentials = true
		default:
			return usageError(
				"usage: kado uninstall --yes [--purge-credentials]",
			)
		}
	}
	if !confirmed {
		return usageError(
			"uninstall requires --yes; credentials are preserved unless --purge-credentials is explicit",
		)
	}
	if purgeCredentials {
		if dependencies.newAuth == nil {
			return authDiagnostic("revoke", errors.New("authentication unavailable"))
		}
		auth, err := dependencies.newAuth()
		if err != nil {
			return authDiagnostic("revoke", err)
		}
		if _, err := auth.Revoke(context.Background()); err != nil {
			return authDiagnostic("revoke", err)
		}
	}
	if dependencies.newRelease == nil {
		return releaseDiagnostic(errors.New("release support unavailable"))
	}
	releases, err := dependencies.newRelease(info)
	if err != nil {
		return releaseDiagnostic(err)
	}
	if err := releases.Uninstall(); err != nil {
		return releaseDiagnostic(err)
	}
	if purgeCredentials {
		_, _ = fmt.Fprintln(
			stdout,
			"removed kado after explicit credential revocation",
		)
	} else {
		_, _ = fmt.Fprintln(stdout, "removed kado; credentials were preserved")
	}
	return nil
}

func releaseDiagnostic(cause error) error {
	code := "release_failed"
	message := "could not verify or install the Kado release"
	switch {
	case errors.Is(cause, releaseclient.ErrDowngrade):
		code = "release_downgrade_blocked"
		message = "a downgrade requires --allow-downgrade"
	case errors.Is(cause, releaseclient.ErrPlatform):
		code = "release_platform_unsupported"
		message = "this release does not support the current platform"
	case errors.Is(cause, releaseclient.ErrUninstall):
		code = "uninstall_failed"
		message = "could not remove the Kado executable; credentials were unchanged"
	}
	return diagnostic.New(code, message, diagnostic.ExitFailure, cause)
}

func runSearch(args []string, stdout io.Writer, dependencies dependencies) error {
	query, options, outputOptions, err := parseSearchArguments(args)
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
		if len(result.canonical) > 0 {
			rendered, renderErr := searchoutput.Render(
				result.canonical,
				result.pages,
				outputOptions,
			)
			if renderErr != nil {
				return searchOutputDiagnostic(renderErr)
			}
			if writeErr := writeOutput(stdout, rendered); writeErr != nil {
				return writeErr
			}
		}
		return searchDiagnostic(err)
	}
	if result.status != searchclient.StatusComplete ||
		len(result.canonical) == 0 ||
		len(result.pages) < 1 {
		return searchDiagnostic(searchclient.ErrProtocol)
	}
	rendered, err := searchoutput.Render(
		result.canonical,
		result.pages,
		outputOptions,
	)
	if err != nil {
		return searchOutputDiagnostic(err)
	}
	return writeOutput(stdout, rendered)
}

func parseSearchArguments(
	args []string,
) (string, searchclient.RunOptions, searchoutput.Options, error) {
	options := searchclient.DefaultRunOptions()
	outputOptions := searchoutput.Options{Mode: searchoutput.ModeHuman}
	var queryParts []string
	var answer string
	answerSet := false
	modeSet := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--":
			queryParts = append(queryParts, args[index+1:]...)
			index = len(args)
		case "--timeout":
			index++
			if index >= len(args) {
				return "", options, outputOptions, usageError("search --timeout requires a duration")
			}
			timeout, err := time.ParseDuration(args[index])
			if err != nil || timeout <= 0 || timeout > 30*time.Minute {
				return "", options, outputOptions, usageError("search timeout must be between 1ns and 30m")
			}
			options.Timeout = timeout
		case "--answer":
			index++
			if index >= len(args) || answerSet {
				return "", options, outputOptions, usageError("search --answer requires one value")
			}
			answer = args[index]
			answerSet = true
		case "--first-page":
			options.FollowPages = false
		case "--retry":
			options.RetryFailure = true
		case "--json", "--jsonl":
			if modeSet {
				return "", options, outputOptions, usageError(
					"search output accepts only one of --json or --jsonl",
				)
			}
			modeSet = true
			if argument == "--json" {
				outputOptions.Mode = searchoutput.ModeJSON
				options.FollowPages = false
			} else {
				outputOptions.Mode = searchoutput.ModeJSONL
			}
		case "--width":
			index++
			if index >= len(args) {
				return "", options, outputOptions, usageError(
					"search --width requires a column count",
				)
			}
			width, err := strconv.Atoi(args[index])
			if err != nil || width < 40 || width > 160 {
				return "", options, outputOptions, usageError(
					"search width must be between 40 and 160 columns",
				)
			}
			outputOptions.Width = width
		default:
			if strings.HasPrefix(argument, "-") {
				return "", options, outputOptions, usageError("unknown search option")
			}
			queryParts = append(queryParts, argument)
		}
	}
	query := strings.Join(queryParts, " ")
	if query == "" {
		return "", options, outputOptions, usageError(
			"usage: kado search [--json|--jsonl] [--width columns] [--timeout duration] [--answer value] [--first-page] [--retry] <query>",
		)
	}
	if answerSet {
		options.Clarify = func(context.Context, searchclient.Question) (string, error) {
			return answer, nil
		}
	}
	return query, options, outputOptions, nil
}

func writeOutput(output io.Writer, value []byte) error {
	written, err := output.Write(value)
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE) {
		return errBrokenPipe
	}
	return diagnostic.New(
		"output_failed",
		"could not write Search output",
		diagnostic.ExitFailure,
		err,
	)
}

func searchOutputDiagnostic(cause error) error {
	var unsupported *searchcontract.UnsupportedVersionError
	if errors.As(cause, &unsupported) {
		return diagnostic.New(
			"search_document_version_unsupported",
			unsupported.Error(),
			diagnostic.ExitFailure,
			cause,
		)
	}
	return diagnostic.New(
		"search_output_invalid",
		"Search output could not be validated or rendered",
		diagnostic.ExitFailure,
		cause,
	)
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
	store, err := newDefaultCredentialStore()
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
		store:  store,
	}, nil
}

func newDefaultSearchCommands() (searchCommands, error) {
	safeConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	store, err := newDefaultCredentialStore()
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
		store:  store,
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

func newDefaultCredentialStore() (keystore.Store, error) {
	return selectDefaultCredentialStore(os.LookupEnv)
}

func selectDefaultCredentialStore(
	environment func(string) (string, bool),
) (keystore.Store, error) {
	path, configured := environment(acceptanceCredentialFileEnvironment)
	if !configured {
		return keystore.NewOSKeychainStore(), nil
	}
	return keystore.NewIsolatedFileStore(path)
}

func newDefaultReleaseCommands(info buildinfo.Info) (releaseCommands, error) {
	if info.ReleasePublicKey == "" ||
		info.ReleaseMetadataURL == "" ||
		info.Version == "" ||
		info.Version == "dev" {
		return nil, releaseclient.ErrInvalidMetadata
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, releaseclient.ErrInstall
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, releaseclient.ErrInstall
	}
	return &defaultReleaseCommands{
		manager: releaseclient.Manager{
			MetadataURL: info.ReleaseMetadataURL,
			PublicKey:   info.ReleasePublicKey,
			Fetcher: releaseclient.HTTPFetcher{
				Client: &http.Client{Timeout: 45 * time.Second},
			},
		},
		executable: executable,
		info:       info,
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

func (commands *defaultSearchCommands) Run(
	ctx context.Context,
	query string,
	options searchclient.RunOptions,
) (searchRunResult, error) {
	result, err := commands.client.Run(ctx, query, options)
	output := searchRunResult{
		status:    result.Document.Status,
		canonical: result.Document.Bytes(),
		pages:     make([][]byte, 0, len(result.Pages)),
	}
	for _, page := range result.Pages {
		output.pages = append(output.pages, page.Bytes())
	}
	return output, err
}

func (commands *defaultReleaseCommands) Update(
	ctx context.Context,
	options releaseclient.Options,
) (releaseclient.Result, error) {
	options.TargetPath = commands.executable
	options.CurrentVersion = commands.info.Version
	return commands.manager.Update(ctx, options)
}

func (commands *defaultReleaseCommands) Uninstall() error {
	return releaseclient.Uninstall(commands.executable)
}

func (commands *defaultReleaseCommands) VerifyBundle(
	directory string,
) (releaseclient.Metadata, releaseclient.Target, error) {
	return releaseclient.VerifyLocalBundle(directory, commands.info)
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
