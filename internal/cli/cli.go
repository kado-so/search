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

	"github.com/kado-so/search/internal/agentapi"
	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/agentidentity"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/config"
	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/keystore"
	"github.com/kado-so/search/internal/localstate"
	"github.com/kado-so/search/internal/maintenance"
	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/requestmeta"
	"github.com/kado-so/search/internal/skillclient"
)

const helpText = `Kado Search command-line client

Usage:
  kado [--agent <identity>] <command>

Commands:
  search <command> Run or manage an authenticated Search
  auth create      Create or authenticate an identity
  auth status      Show identity state
  auth revoke      Revoke an identity
  auth identities  List locally known identities
  agent detect     Show the detected agent
  agent list       List supported agents
  skill <command>   Manage the Search skill
  update            Install a signed CLI release
  uninstall         Remove the CLI
  release verify    Verify a release
  help              Show help
  version           Show build information

Options:
  --agent identity   Explicitly select the calling agent identity
  --json            Emit compact agent-cli-json.v1 JSON
  -h, --help        Show this help
  -v, --version     Show bounded build information
  version --json    Show executable provenance
`

type authCommands interface {
	Status(context.Context) (agentauth.CredentialStatus, error)
	Revoke(context.Context) (agentauth.CredentialStatus, error)
}

type authCreator interface {
	Create(context.Context) (agentauth.CredentialStatus, error)
}

type searchCommands interface {
	Start(context.Context, agentapi.StartRequest) (agentapi.Response, error)
	Status(context.Context, string, agentapi.WaitOptions, agentapi.ResultLimits) (agentapi.Response, error)
	Refine(context.Context, agentapi.RefineRequest) (agentapi.Response, error)
	Answer(context.Context, agentapi.AnswerRequest) (agentapi.Response, error)
	Cancel(context.Context, agentapi.CancelRequest) (agentapi.Response, error)
}

type releaseCommands interface {
	Update(context.Context, releaseclient.Options) (releaseclient.Result, error)
	Uninstall() error
	VerifyBundle(string) (releaseclient.Metadata, releaseclient.Target, error)
}

type releaseChecker interface {
	Check(context.Context, string) (releaseclient.Result, error)
}

type skillCommands interface {
	Install(context.Context, skillclient.InstallOptions) (skillclient.InstallResult, error)
	Update(context.Context) (skillclient.UpdateResult, error)
	Status() (skillclient.Status, error)
	Uninstall([]string, bool) ([]skillclient.Installation, error)
}

type dependencies struct {
	detectAgent    func(string) (agentidentity.Detection, error)
	listIdentities func() ([]string, error)
	newAuth        func(string) (authCommands, error)
	newSearch      func(string, searchConnectionOptions) (searchCommands, error)
	newRelease     func(buildinfo.Info) (releaseCommands, error)
	newSkill       func(buildinfo.Info) (skillCommands, error)
}

type defaultAuthCommands struct {
	client    *agentauth.Client
	store     keystore.Store
	configDir string
	agent     string
}

type defaultSearchCommands struct {
	client *agentapi.Client
}

type defaultReleaseCommands struct {
	manager    releaseclient.Manager
	executable string
	info       buildinfo.Info
}

type agentAuthorizationSource struct {
	client    *agentauth.Client
	store     keystore.Store
	token     agentauth.SessionToken
	configDir string
	agent     string
}

var outputIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)
var errBrokenPipe = errors.New("CLI output pipe closed")

// Run executes one CLI invocation and returns a process exit status.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return runWithDependencies(args, stdout, stderr, info, dependencies{
		detectAgent:    agentidentity.Detect,
		listIdentities: listDefaultIdentities,
		newAuth:        newDefaultAuthCommands,
		newSearch:      newDefaultSearchCommands,
		newRelease:     newDefaultReleaseCommands,
		newSkill:       newDefaultSkillCommands,
	})
}

func runWithDependencies(
	args []string,
	stdout,
	stderr io.Writer,
	info buildinfo.Info,
	dependencies dependencies,
) int {
	maybeScheduleMaintenance(args, stderr, info)
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

func maybeScheduleMaintenance(args []string, stderr io.Writer, info buildinfo.Info) {
	if info.Version == "" || info.Version == "dev" ||
		info.ReleasePublicKey == "" || info.ReleaseMetadataURL == "" ||
		os.Getenv("KADO_MAINTENANCE_CHILD") != "" ||
		len(args) >= 3 && args[0] == "skill" && args[1] == "update" &&
			args[2] == "--background" {
		return
	}
	safeConfig, err := config.Load()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	state, err := maintenance.Read(safeConfig.ConfigDir)
	if err != nil {
		return
	}
	if maintenance.NoticeDue(state, now) {
		_, _ = fmt.Fprintf(
			stderr,
			"kado: update %s is available; run `kado update`\n",
			state.LatestCLIVersion,
		)
		state.LastNoticeAt = now.Format(time.RFC3339)
		_ = maintenance.Write(safeConfig.ConfigDir, state)
	}
	if !maintenance.Due(state, now) {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	// Reserve a short window before spawning so concurrent CLI invocations do
	// not create duplicate maintenance workers.
	state.NextCheckAt = now.Add(10 * time.Minute).Format(time.RFC3339)
	if err := maintenance.Write(safeConfig.ConfigDir, state); err != nil {
		return
	}
	_ = maintenance.Spawn(executable)
}

func run(
	args []string,
	stdout io.Writer,
	info buildinfo.Info,
	dependencies dependencies,
) error {
	override, args, err := parseGlobalOptions(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		_, _ = io.WriteString(stdout, helpText)
		return nil
	}

	switch args[0] {
	case "__update-helper":
		if err := releaseclient.RunWindowsUpdateHelper(args[1:]); err != nil {
			return releaseDiagnostic(err)
		}
		return nil
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
		return runAuth(args[1:], stdout, override, dependencies)
	case "search":
		return runSearch(args[1:], stdout, override, info, dependencies)
	case "agent":
		return runAgent(args[1:], stdout, override, dependencies)
	case "skill":
		return runSkill(args[1:], stdout, info, override, dependencies)
	case "update":
		return runUpdate(args[1:], stdout, info, dependencies)
	case "uninstall":
		return runUninstall(args[1:], stdout, info, override, dependencies)
	case "release":
		return runRelease(args[1:], stdout, info, dependencies)
	default:
		return usageError("unknown command; run 'kado help' for usage")
	}
}

func runSkill(
	args []string,
	stdout io.Writer,
	info buildinfo.Info,
	override string,
	dependencies dependencies,
) error {
	if len(args) == 0 {
		return usageError("usage: kado skill install|status|update|uninstall")
	}
	if dependencies.newSkill == nil {
		return skillDiagnostic(errors.New("skill support unavailable"))
	}
	commands, err := dependencies.newSkill(info)
	if err != nil {
		return skillDiagnostic(err)
	}
	switch args[0] {
	case "install":
		detection, err := resolveAgent(override, dependencies)
		if err != nil {
			return err
		}
		options := skillclient.InstallOptions{CurrentAgent: detection.Agent}
		for index := 1; index < len(args); index++ {
			switch args[index] {
			case "--all":
				options.All = true
			case "--agent":
				if index+1 >= len(args) {
					return usageError("--agent requires one identity")
				}
				index++
				if !agentidentity.Valid(args[index]) {
					return usageError("unknown agent identity")
				}
				options.Agents = append(options.Agents, args[index])
			default:
				return usageError("usage: kado skill install [--all] [--agent <identity>]")
			}
		}
		result, err := commands.Install(context.Background(), options)
		if err != nil {
			return skillDiagnostic(err)
		}
		for _, installed := range result.Installed {
			_, _ = fmt.Fprintf(
				stdout,
				"installed kado-search %s for %s at %s\n",
				installed.Version,
				installed.Agent,
				installed.Path,
			)
		}
		if !options.All && len(options.Agents) == 0 && len(result.OtherAgents) > 0 {
			_, _ = fmt.Fprintf(
				stdout,
				"also detected %s; install for all detected agents? After approval, run `kado skill install --all`\n",
				strings.Join(result.OtherAgents, ", "),
			)
		}
		if result.UsedFallback {
			_, _ = fmt.Fprintln(stdout, "installed the bundled offline skill fallback")
		}
		return nil
	case "status":
		if len(args) != 1 {
			return usageError("usage: kado skill status")
		}
		status, err := commands.Status()
		if err != nil {
			return skillDiagnostic(err)
		}
		if len(status.Installations) == 0 {
			_, _ = fmt.Fprintln(stdout, "no Kado-managed skills are installed")
			return nil
		}
		for _, installed := range status.Installations {
			_, _ = fmt.Fprintf(
				stdout,
				"kado-search %s agent=%s path=%s\n",
				installed.Version,
				installed.Agent,
				installed.Path,
			)
		}
		return nil
	case "update":
		background := len(args) == 2 && args[1] == "--background"
		if len(args) != 1 && !background {
			return usageError("usage: kado skill update")
		}
		result, err := commands.Update(context.Background())
		if err != nil {
			return skillDiagnostic(err)
		}
		if background {
			latest := info.Version
			available := false
			if dependencies.newRelease != nil {
				if releases, releaseErr := dependencies.newRelease(info); releaseErr == nil {
					if checker, ok := releases.(releaseChecker); ok {
						check, checkErr := checker.Check(context.Background(), info.Version)
						if checkErr == nil {
							latest = check.ToVersion
							available = check.Changed
						}
					}
				}
			}
			if safeConfig, configErr := config.Load(); configErr == nil {
				state, _ := maintenance.Read(safeConfig.ConfigDir)
				state = maintenance.Complete(
					state,
					time.Now(),
					info.Version,
					latest,
					available,
				)
				_ = maintenance.Write(safeConfig.ConfigDir, state)
			}
			return nil
		}
		for _, updated := range result.Updated {
			_, _ = fmt.Fprintf(
				stdout,
				"updated kado-search to %s for %s\n",
				updated.Version,
				updated.Agent,
			)
		}
		if len(result.Updated) == 0 && len(result.Failures) == 0 {
			_, _ = fmt.Fprintln(stdout, "Kado-managed skills are up to date")
		}
		for path, code := range result.Failures {
			_, _ = fmt.Fprintf(stdout, "could not update %s (%s)\n", path, code)
		}
		return nil
	case "uninstall":
		all := false
		agents := []string(nil)
		for index := 1; index < len(args); index++ {
			switch args[index] {
			case "--all":
				all = true
			case "--agent":
				if index+1 >= len(args) {
					return usageError("--agent requires one identity")
				}
				index++
				agents = append(agents, args[index])
			default:
				return usageError("usage: kado skill uninstall [--all] [--agent <identity>]")
			}
		}
		if !all && len(agents) == 0 {
			detection, err := resolveAgent(override, dependencies)
			if err != nil {
				return err
			}
			agents = []string{detection.Agent}
		}
		removed, err := commands.Uninstall(agents, all)
		if err != nil {
			return skillDiagnostic(err)
		}
		for _, item := range removed {
			_, _ = fmt.Fprintf(stdout, "removed kado-search for %s\n", item.Agent)
		}
		return nil
	default:
		return usageError("usage: kado skill install|status|update|uninstall")
	}
}

func skillDiagnostic(cause error) error {
	message := "could not install or update the Kado Search skill"
	switch {
	case errors.Is(cause, skillclient.ErrUnsupportedAgent):
		message = "the detected agent does not have a supported skill location"
	case errors.Is(cause, skillclient.ErrExternallyManaged):
		message = "the skill destination is managed by another installer"
	case errors.Is(cause, skillclient.ErrLocallyModified):
		message = "the Kado-managed skill was modified locally"
	case errors.Is(cause, skillclient.ErrUnsupportedCLI):
		message = "the latest skill requires a newer Kado CLI"
	}
	return diagnostic.New("skill_failed", message, diagnostic.ExitFailure, cause)
}

func parseGlobalOptions(args []string) (string, []string, error) {
	override := ""
	for len(args) > 0 {
		switch {
		case args[0] == "--agent":
			if len(args) < 2 || override != "" {
				return "", nil, usageError("--agent requires one identity")
			}
			override = args[1]
			args = args[2:]
		case strings.HasPrefix(args[0], "--agent="):
			if override != "" {
				return "", nil, usageError("--agent accepts one identity")
			}
			override = strings.TrimPrefix(args[0], "--agent=")
			if override == "" {
				return "", nil, usageError("--agent requires one identity")
			}
			args = args[1:]
		default:
			return override, args, nil
		}
	}
	return override, args, nil
}

func resolveAgent(
	override string,
	dependencies dependencies,
) (agentidentity.Detection, error) {
	if dependencies.detectAgent != nil {
		return dependencies.detectAgent(override)
	}
	if override != "" {
		if !agentidentity.Valid(override) {
			return agentidentity.Detection{}, usageError("unknown agent identity")
		}
		return agentidentity.Detection{Agent: override, Source: "override"}, nil
	}
	return agentidentity.Detection{Agent: agentidentity.Default, Source: "default"}, nil
}

func runAgent(
	args []string,
	stdout io.Writer,
	override string,
	dependencies dependencies,
) error {
	if len(args) != 1 {
		return usageError("usage: kado agent <detect|list>")
	}
	switch args[0] {
	case "list":
		for _, agent := range agentidentity.Known() {
			_, _ = fmt.Fprintln(stdout, agent)
		}
		return nil
	case "detect":
		detection, err := resolveAgent(override, dependencies)
		if err != nil {
			return usageError(err.Error())
		}
		_, _ = fmt.Fprintf(
			stdout,
			"agent: %s\nsource: %s\n",
			detection.Agent,
			detection.Source,
		)
		return nil
	default:
		return usageError("unknown agent command; use 'detect' or 'list'")
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
	case result.Pending:
		_, _ = fmt.Fprintf(
			stdout,
			"verified kado %s; Windows will finish the update after this process exits\n",
			result.ToVersion,
		)
	case result.Changed:
		scheduleMaintenanceNow()
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

func scheduleMaintenanceNow() {
	safeConfig, err := config.Load()
	if err != nil {
		return
	}
	state, _ := maintenance.Read(safeConfig.ConfigDir)
	state.NextCheckAt = time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if err := maintenance.Write(safeConfig.ConfigDir, state); err != nil {
		return
	}
	executable, err := os.Executable()
	if err == nil {
		_ = maintenance.Spawn(executable)
	}
}

func runUninstall(
	args []string,
	stdout io.Writer,
	info buildinfo.Info,
	override string,
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
		detection, err := resolveAgent(override, dependencies)
		if err != nil {
			return usageError(err.Error())
		}
		auth, err := dependencies.newAuth(detection.Agent)
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

func runSearch(
	args []string,
	stdout io.Writer,
	override string,
	info buildinfo.Info,
	dependencies dependencies,
) error {
	request, err := parseSearchArguments(args)
	if err != nil {
		return err
	}
	if dependencies.newSearch == nil {
		return searchDiagnostic(errors.New("Search unavailable"))
	}
	detection, err := resolveAgent(override, dependencies)
	if err != nil {
		return usageError(err.Error())
	}
	search, err := dependencies.newSearch(detection.Agent, request.connection)
	if err != nil {
		return searchDiagnostic(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var response agentapi.Response
	switch request.operation {
	case searchStart:
		response, err = search.Start(ctx, agentapi.StartRequest{
			Query: request.query, Wait: request.wait, Limits: request.limits,
			Version: info.Version,
		})
	case searchStatus:
		response, err = search.Status(ctx, request.searchID, request.wait, request.limits)
	case searchRefine:
		response, err = search.Refine(ctx, agentapi.RefineRequest{
			SearchID: request.searchID, Dimensions: request.dimensions,
			Wait: request.wait, Limits: request.limits,
		})
	case searchAnswer:
		response, err = search.Answer(ctx, agentapi.AnswerRequest{
			SearchID: request.searchID, Answers: request.answers,
			Wait: request.wait, Limits: request.limits,
		})
	case searchCancel:
		response, err = search.Cancel(ctx, agentapi.CancelRequest{
			SearchID: request.searchID, Reason: request.reason,
		})
	default:
		return searchDiagnostic(agentapi.ErrProtocol)
	}
	if len(response.ResultJSON) > 0 {
		output := response.ResultJSON
		if !request.json {
			output = renderAgentResult(response.Result)
		}
		if writeErr := writeOutput(stdout, output); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return searchDiagnostic(err)
	}
	if len(response.ResultJSON) == 0 {
		return searchDiagnostic(agentapi.ErrProtocol)
	}
	return nil
}

type searchOperation uint8

const (
	searchStart searchOperation = iota
	searchStatus
	searchRefine
	searchAnswer
	searchCancel
)

type searchConnectionOptions struct {
	baseURL string
	apiKey  string
}

type searchRequest struct {
	operation  searchOperation
	query      string
	searchID   string
	dimensions []agentapi.DimensionUpdate
	answers    []agentapi.Answer
	reason     string
	json       bool
	wait       agentapi.WaitOptions
	limits     agentapi.ResultLimits
	connection searchConnectionOptions
}

func parseSearchArguments(args []string) (searchRequest, error) {
	request := searchRequest{
		operation: searchStart,
		reason:    "agent_no_longer_needs_result",
		wait:      agentapi.WaitOptions{TimeoutMS: 120_000, PollIntervalMS: 2_000},
	}
	if len(args) > 0 {
		switch args[0] {
		case "status":
			request.operation, args = searchStatus, args[1:]
		case "refine":
			request.operation, args = searchRefine, args[1:]
		case "answer":
			request.operation, args = searchAnswer, args[1:]
		case "cancel":
			request.operation, args = searchCancel, args[1:]
		}
	}
	var positionals []string
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--":
			positionals = append(positionals, args[index+1:]...)
			index = len(args)
		case "--json":
			request.json = true
		case "--wait":
			if request.operation == searchCancel {
				return request, usageError("search cancel does not accept --wait")
			}
			request.wait.Enabled = true
		case "--timeout":
			index++
			if index >= len(args) {
				return request, usageError("search --timeout requires a duration")
			}
			timeout, err := time.ParseDuration(args[index])
			if err != nil || timeout < 0 || timeout > 2*time.Minute {
				return request, usageError("search timeout must be between 0s and 2m")
			}
			request.wait.TimeoutMS = int(timeout / time.Millisecond)
		case "--timeout-ms":
			value, next, err := integerOption(args, index, argument, 0, 120_000)
			if err != nil {
				return request, err
			}
			index = next
			request.wait.TimeoutMS = value
		case "--poll-interval":
			index++
			if index >= len(args) {
				return request, usageError("search --poll-interval requires a duration")
			}
			value, err := time.ParseDuration(args[index])
			if err != nil || value < 100*time.Millisecond || value > 2*time.Minute {
				return request, usageError("search poll interval must be between 100ms and 2m")
			}
			request.wait.PollIntervalMS = int(value / time.Millisecond)
		case "--poll-interval-ms":
			value, next, err := integerOption(args, index, argument, 100, 120_000)
			if err != nil {
				return request, err
			}
			index = next
			request.wait.PollIntervalMS = value
		case "--base-url":
			index++
			if index >= len(args) || request.connection.baseURL != "" {
				return request, usageError("search --base-url requires one value")
			}
			request.connection.baseURL = args[index]
		case "--api-key":
			index++
			if index >= len(args) || request.connection.apiKey != "" {
				return request, usageError("search --api-key requires one value")
			}
			request.connection.apiKey = args[index]
		case "--dimension":
			if request.operation != searchRefine {
				return request, usageError("--dimension is valid only for search refine")
			}
			index++
			if index >= len(args) {
				return request, usageError("search --dimension requires name=value")
			}
			name, value, ok := strings.Cut(args[index], "=")
			name, value = strings.TrimSpace(name), strings.TrimSpace(value)
			if !ok || name == "" || value == "" {
				return request, usageError("search --dimension requires name=value")
			}
			request.dimensions = append(request.dimensions, agentapi.DimensionUpdate{ID: name, Value: value})
		case "--reason":
			if request.operation != searchCancel {
				return request, usageError("--reason is valid only for search cancel")
			}
			index++
			if index >= len(args) {
				return request, usageError("search --reason requires one value")
			}
			request.reason = args[index]
		case "--max-best-matches", "--max-stretch-matches", "--max-later-matches", "--later-offset":
			value, next, err := integerOption(args, index, argument, 0, 100)
			if err != nil {
				return request, err
			}
			index = next
			switch argument {
			case "--max-best-matches":
				request.limits.BestMatches = value
			case "--max-stretch-matches":
				request.limits.StretchMatches = value
			case "--max-later-matches":
				request.limits.LaterMatches = value
			case "--later-offset":
				request.limits.LaterOffset = value
			}
		default:
			if strings.HasPrefix(argument, "-") {
				return request, usageError("unknown search option")
			}
			positionals = append(positionals, argument)
		}
	}
	if request.operation == searchCancel && request.wait.Enabled {
		return request, usageError("search cancel does not accept --wait")
	}
	switch request.operation {
	case searchStart:
		request.query = strings.TrimSpace(strings.Join(positionals, " "))
		if len(request.query) < 3 || len(request.query) > 4000 {
			return request, usageError("Search query must be between 3 and 4000 characters")
		}
	case searchStatus, searchRefine, searchCancel:
		if len(positionals) != 1 || !validSearchIdentifier(positionals[0]) {
			return request, usageError("search command requires one valid search ID")
		}
		request.searchID = positionals[0]
		if request.operation == searchRefine && (len(request.dimensions) < 1 || len(request.dimensions) > 50) {
			return request, usageError("search refine requires 1 to 50 --dimension name=value flags")
		}
	case searchAnswer:
		if len(positionals) < 3 || !validSearchIdentifier(positionals[0]) || !validSearchIdentifier(positionals[1]) {
			return request, usageError("search answer requires <search-id> <question-id> <answer>")
		}
		answer := strings.TrimSpace(strings.Join(positionals[2:], " "))
		if answer == "" || len(answer) > 4000 {
			return request, usageError("search answer must be between 1 and 4000 characters")
		}
		request.searchID = positionals[0]
		request.answers = []agentapi.Answer{{QuestionID: positionals[1], Answer: answer}}
	}
	return request, nil
}

func integerOption(args []string, index int, name string, minimum, maximum int) (int, int, error) {
	if index+1 >= len(args) {
		return 0, index, usageError(name + " requires an integer")
	}
	value, err := strconv.Atoi(args[index+1])
	if err != nil || value < minimum || value > maximum {
		return 0, index, usageError(fmt.Sprintf("%s must be between %d and %d", name, minimum, maximum))
	}
	return value, index + 1, nil
}

func validSearchIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || index > 0 && strings.ContainsRune("._~-", character) {
			continue
		}
		return false
	}
	return true
}

func renderAgentResult(result agentapi.Result) []byte {
	var output strings.Builder
	id := ""
	if result.SearchID != nil {
		id = *result.SearchID
	}
	if result.Error != nil {
		_, _ = fmt.Fprintf(&output, "Search failed: %s\n", result.Error.Message)
		if id != "" {
			_, _ = fmt.Fprintf(&output, "Search ID: %s\n", id)
		}
		return []byte(output.String())
	}
	switch result.State {
	case "completed":
		_, _ = fmt.Fprintf(&output, "Search completed: %s\n", id)
		if result.SearchURL != nil && *result.SearchURL != "" {
			_, _ = fmt.Fprintf(&output, "Kado search: %s\n", *result.SearchURL)
		}
		for _, match := range result.BestMatches {
			_, _ = fmt.Fprintf(&output, "%d. %s", match.Rank, match.Name)
			if match.Score > 0 {
				_, _ = fmt.Fprintf(&output, " (score %d)", match.Score)
			}
			output.WriteByte('\n')
			if match.SolutionURL != "" {
				_, _ = fmt.Fprintf(&output, "   Link: %s\n", match.SolutionURL)
			}
			if match.Summary != "" {
				_, _ = fmt.Fprintf(&output, "   %s\n", match.Summary)
			}
		}
		if len(result.Questions) > 0 {
			output.WriteString("Questions:\n")
			for _, question := range result.Questions {
				_, _ = fmt.Fprintf(&output, "- %s: %s\n", question.ID, question.Prompt)
			}
		}
	case "canceled":
		_, _ = fmt.Fprintf(&output, "Search canceled: %s\n", id)
	default:
		_, _ = fmt.Fprintf(&output, "Search %s is %s.\n", id, result.State)
		if result.Continuation.NextCommand != nil && *result.Continuation.NextCommand != "" {
			_, _ = fmt.Fprintf(&output, "Next: %s\n", *result.Continuation.NextCommand)
		}
	}
	return []byte(output.String())
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

func runAuth(
	args []string,
	stdout io.Writer,
	override string,
	dependencies dependencies,
) error {
	if len(args) != 1 {
		return usageError("usage: kado auth <create|status|revoke|identities>")
	}
	if args[0] == "identities" {
		if dependencies.listIdentities == nil {
			return authDiagnostic("status", errors.New("identity list unavailable"))
		}
		identities, err := dependencies.listIdentities()
		if err != nil {
			return authDiagnostic("status", err)
		}
		if len(identities) == 0 {
			_, _ = fmt.Fprintln(stdout, "none")
			return nil
		}
		for _, identity := range identities {
			_, _ = fmt.Fprintln(stdout, identity)
		}
		return nil
	}
	if args[0] != "create" && args[0] != "status" && args[0] != "revoke" {
		return usageError(
			"unknown auth command; use 'create', 'status', 'revoke', or 'identities'",
		)
	}
	if dependencies.newAuth == nil {
		return authDiagnostic(args[0], errors.New("authentication unavailable"))
	}
	detection, err := resolveAgent(override, dependencies)
	if err != nil {
		return usageError(err.Error())
	}
	auth, err := dependencies.newAuth(detection.Agent)
	if err != nil {
		return authDiagnostic(args[0], err)
	}
	var status agentauth.CredentialStatus
	if args[0] == "create" {
		creator, ok := auth.(authCreator)
		if !ok {
			return authDiagnostic("create", errors.New("identity creation unavailable"))
		}
		status, err = creator.Create(context.Background())
	} else if args[0] == "status" {
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
			"the prior agent identity was revoked, but the local credential changed; retry to revoke the selected identity",
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
			"could not revoke the selected agent identity",
			diagnostic.ExitFailure,
			cause,
		)
	}
	if operation == "create" {
		return diagnostic.New(
			"auth_create_failed",
			"could not create or authenticate the selected agent identity",
			diagnostic.ExitFailure,
			cause,
		)
	}
	return diagnostic.New(
		"auth_status_failed",
		"could not read the selected agent identity status",
		diagnostic.ExitFailure,
		cause,
	)
}

func searchDiagnostic(cause error) error {
	if errors.Is(cause, context.Canceled) {
		return diagnostic.New(
			"search_interrupted",
			"Search was interrupted",
			diagnostic.ExitFailure,
			cause,
		)
	}
	var remote *agentapi.Error
	if errors.As(cause, &remote) {
		code := remote.Code
		if code == "" {
			code = "agent_api_failed"
		}
		return diagnostic.New(code, remote.Error(), diagnostic.ExitFailure, cause)
	}
	if errors.Is(cause, agentapi.ErrAuthentication) {
		return diagnostic.New(
			"agent_auth_required",
			"Search authentication is unavailable; create an identity or set KADO_API_KEY",
			diagnostic.ExitFailure,
			cause,
		)
	}
	if errors.Is(cause, agentapi.ErrProtocol) {
		return diagnostic.New(
			"agent_contract_invalid",
			"kado-app returned an invalid agent-api.v1 response",
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

func newDefaultAuthCommands(agent string) (authCommands, error) {
	safeConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	host, err := localstate.EnsureHost(safeConfig.ConfigDir)
	if err != nil {
		return nil, err
	}
	store, err := selectCredentialStore(safeConfig, agent)
	if err != nil {
		return nil, err
	}
	httpClient := serviceHTTPClient(
		30*time.Second,
		agentauth.DefaultLimits().MaxResponseHeaderBytes,
		agent,
		host.ID,
	)
	client, err := agentauth.NewClient(
		safeConfig.BaseURL,
		httpClient,
		agentauth.DefaultLimits(),
		rand.Reader,
	)
	if err != nil {
		return nil, err
	}
	return &defaultAuthCommands{
		client:    client,
		store:     store,
		configDir: safeConfig.ConfigDir,
		agent:     agent,
	}, nil
}

func newDefaultSearchCommands(agent string, options searchConnectionOptions) (searchCommands, error) {
	safeConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	baseURL := safeConfig.BaseURL
	rawBaseURL := strings.TrimSpace(options.baseURL)
	if rawBaseURL == "" {
		rawBaseURL = strings.TrimSpace(os.Getenv("KADO_SEARCH_APP_URL"))
	}
	if rawBaseURL == "" {
		rawBaseURL = strings.TrimSpace(os.Getenv("KADO_BASE_URL"))
	}
	if rawBaseURL != "" {
		baseURL, err = config.ParseBaseURL(rawBaseURL)
		if err != nil {
			return nil, err
		}
	}
	host, err := localstate.EnsureHost(safeConfig.ConfigDir)
	if err != nil {
		return nil, err
	}
	httpClient := serviceHTTPClient(
		130*time.Second,
		agentauth.DefaultLimits().MaxResponseHeaderBytes,
		agent,
		host.ID,
	)
	apiKey := options.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("KADO_API_KEY")
	}
	var authorization agentapi.AuthorizationSource
	if apiKey == "" {
		store, storeErr := selectCredentialStore(safeConfig, agent)
		if storeErr != nil {
			return nil, storeErr
		}
		authClient, authErr := agentauth.NewClient(baseURL, httpClient, agentauth.DefaultLimits(), rand.Reader)
		if authErr != nil {
			return nil, authErr
		}
		authorization = &agentAuthorizationSource{
			client: authClient, store: store, configDir: safeConfig.ConfigDir, agent: agent,
		}
	}
	search, err := agentapi.New(agentapi.Options{
		BaseURL: baseURL, HTTPClient: httpClient, Authorization: authorization,
		APIKey: apiKey, UserAgent: "kado-cli",
	})
	if err != nil {
		return nil, err
	}
	return &defaultSearchCommands{client: search}, nil
}

func selectCredentialStore(
	safeConfig config.Config,
	agent string,
) (keystore.Store, error) {
	switch safeConfig.CredentialBackend {
	case config.CredentialBackendOS:
		return keystore.NewOSKeychainStore(agent)
	case config.CredentialBackendFile:
		return keystore.NewAgentFileStore(safeConfig.SecretsDir, agent)
	default:
		return nil, errors.New("unsupported credential backend")
	}
}

func listDefaultIdentities() ([]string, error) {
	safeConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	return localstate.ListIdentities(safeConfig.ConfigDir)
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
				Client: boundedHTTPClient(45*time.Second, 64*1024),
			},
		},
		executable: executable,
		info:       info,
	}, nil
}

func newDefaultSkillCommands(info buildinfo.Info) (skillCommands, error) {
	safeConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &skillclient.Manager{
		ConfigDir:      safeConfig.ConfigDir,
		HomeDir:        home,
		BaseURL:        strings.TrimSuffix(safeConfig.BaseURL.String(), "/"),
		PublicKey:      info.ReleasePublicKey,
		CurrentVersion: info.Version,
		Fetcher: releaseclient.HTTPFetcher{
			Client: boundedHTTPClient(15*time.Second, 64*1024),
		},
	}, nil
}

func boundedHTTPClient(timeout time.Duration, maxResponseHeaderBytes int64) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = maxResponseHeaderBytes
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func serviceHTTPClient(
	timeout time.Duration,
	maxResponseHeaderBytes int64,
	agent string,
	hostID string,
) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = maxResponseHeaderBytes
	return &http.Client{
		Timeout:   timeout,
		Transport: requestmeta.NewTransport(transport, agent, hostID),
	}
}

func (commands *defaultAuthCommands) Create(
	ctx context.Context,
) (agentauth.CredentialStatus, error) {
	result, err := commands.client.AuthenticateOrEnroll(
		ctx,
		commands.store,
		agentauth.Request{Mode: agentauth.CreateIfMissing},
	)
	if err != nil {
		return agentauth.CredentialStatus{}, err
	}
	if err := localstate.AddIdentity(commands.configDir, commands.agent); err != nil {
		return agentauth.CredentialStatus{}, err
	}
	return agentauth.CredentialStatus{
		Status:       agentauth.StatusActive,
		PrincipalID:  result.PrincipalID,
		CredentialID: result.CredentialID,
		ClientID:     result.ClientID,
	}, nil
}

func (commands *defaultAuthCommands) Status(
	ctx context.Context,
) (agentauth.CredentialStatus, error) {
	status, err := commands.client.CredentialStatus(ctx, commands.store)
	if err == nil && status.Status == agentauth.StatusActive {
		err = localstate.AddIdentity(commands.configDir, commands.agent)
	} else if errors.Is(err, agentauth.ErrCredentialNotFound) {
		_ = localstate.RemoveIdentity(commands.configDir, commands.agent)
	}
	return status, err
}

func (commands *defaultAuthCommands) Revoke(
	ctx context.Context,
) (agentauth.CredentialStatus, error) {
	status, err := commands.client.RevokeCurrentCredential(ctx, commands.store)
	if err == nil {
		err = localstate.RemoveIdentity(commands.configDir, commands.agent)
	}
	return status, err
}

func (commands *defaultSearchCommands) Start(ctx context.Context, request agentapi.StartRequest) (agentapi.Response, error) {
	return commands.client.Start(ctx, request)
}
func (commands *defaultSearchCommands) Status(ctx context.Context, id string, wait agentapi.WaitOptions, limits agentapi.ResultLimits) (agentapi.Response, error) {
	return commands.client.Status(ctx, id, wait, limits)
}
func (commands *defaultSearchCommands) Refine(ctx context.Context, request agentapi.RefineRequest) (agentapi.Response, error) {
	return commands.client.Refine(ctx, request)
}
func (commands *defaultSearchCommands) Answer(ctx context.Context, request agentapi.AnswerRequest) (agentapi.Response, error) {
	return commands.client.Answer(ctx, request)
}
func (commands *defaultSearchCommands) Cancel(ctx context.Context, request agentapi.CancelRequest) (agentapi.Response, error) {
	return commands.client.Cancel(ctx, request)
}

func (commands *defaultReleaseCommands) Update(
	ctx context.Context,
	options releaseclient.Options,
) (releaseclient.Result, error) {
	options.TargetPath = commands.executable
	options.CurrentVersion = commands.info.Version
	return commands.manager.Update(ctx, options)
}

func (commands *defaultReleaseCommands) Check(
	ctx context.Context,
	currentVersion string,
) (releaseclient.Result, error) {
	return commands.manager.Check(ctx, currentVersion)
}

func (commands *defaultReleaseCommands) Uninstall() error {
	return releaseclient.Uninstall(commands.executable)
}

func (commands *defaultReleaseCommands) VerifyBundle(
	directory string,
) (releaseclient.Metadata, releaseclient.Target, error) {
	return releaseclient.VerifyLocalBundle(directory, commands.info)
}

func (source *agentAuthorizationSource) Authorization(
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
	if err := localstate.AddIdentity(source.configDir, source.agent); err != nil {
		return "", err
	}
	source.token = token
	return source.token.AuthorizationHeader(), nil
}

func usageError(message string) error {
	return diagnostic.New("invalid_usage", message, diagnostic.ExitUsage, nil)
}
