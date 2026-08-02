package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/kado-so/search/internal/agentapi"
	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/agentidentity"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/skillclient"
)

func TestHelpFormsAreBoundedAndSilentOnStderr(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(args, &stdout, &stderr, buildinfo.Info{})

		if exitCode != 0 {
			t.Fatalf("Run(%q) exit code = %d", args, exitCode)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Run(%q) stderr = %q", args, stderr.String())
		}
		if stdout.Len() == 0 || stdout.Len() > 1024 {
			t.Fatalf("Run(%q) help length = %d", args, stdout.Len())
		}
	}
}

func TestVersionFormsAreSingleLineAndBounded(t *testing.T) {
	t.Parallel()

	info := buildinfo.Info{Version: strings.Repeat("v", 100), Commit: "abc\nsecret", Date: "now"}
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(args, &stdout, &stderr, info)

		if exitCode != 0 {
			t.Fatalf("Run(%q) exit code = %d", args, exitCode)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Run(%q) stderr = %q", args, stderr.String())
		}
		if strings.Count(stdout.String(), "\n") != 1 || stdout.Len() > 181 {
			t.Fatalf("Run(%q) version output = %q", args, stdout.String())
		}
	}
}

func TestVersionJSONIsDeterministicExecutableProvenance(t *testing.T) {
	t.Parallel()

	info := buildinfo.Info{
		Version:          "0.1.0",
		Commit:           "0123456789abcdef",
		Date:             "2026-07-24T00:00:00Z",
		Target:           "linux/arm64",
		ReleaseKeyID:     "sha256:abc",
		ReleasePublicKey: "public",
	}
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"version", "--json"}, &stdout, &stderr, info)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exitCode, stderr.String())
	}
	want := "{\"version\":\"0.1.0\",\"commit\":\"0123456789abcdef\",\"built_at\":\"2026-07-24T00:00:00Z\",\"target\":\"linux/arm64\",\"release_key_id\":\"sha256:abc\",\"release_public_key\":\"public\"}\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestInvalidUsageIsSafeAndUsesUsageExitCode(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"unknown"}, {"help", "extra"}, {"version", "extra"}} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(args, &stdout, &stderr, buildinfo.Info{})

		if exitCode != diagnostic.ExitUsage {
			t.Fatalf("Run(%q) exit code = %d", args, exitCode)
		}
		if stdout.Len() != 0 {
			t.Fatalf("Run(%q) stdout = %q", args, stdout.String())
		}
		if stderr.Len() == 0 || stderr.Len() > 512 || strings.Count(stderr.String(), "\n") != 1 {
			t.Fatalf("Run(%q) stderr = %q", args, stderr.String())
		}
	}
}

func TestAuthStatusPrintsOnlyBoundedNonSecretIdentityState(t *testing.T) {
	t.Parallel()

	auth := &fakeAuthCommands{
		status: agentauth.CredentialStatus{
			Status:       agentauth.StatusActive,
			PrincipalID:  "agt_123",
			CredentialID: "acred_456",
			ClientID:     "clt_789",
		},
	}
	stdout, stderr, exitCode := runTestAuth(t, []string{"auth", "status"}, auth)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("status exit=%d stderr=%q", exitCode, stderr)
	}
	if stdout != "status: active\nprincipal: agt_123\ncredential: acred_456\nclient: clt_789\n" {
		t.Fatalf("status stdout = %q", stdout)
	}
	assertNoCredentialSecrets(t, stdout+stderr)
}

func TestAgentOverrideSelectsNamespacedAuthFactory(t *testing.T) {
	t.Parallel()

	selected := ""
	auth := &fakeAuthCommands{
		status: agentauth.CredentialStatus{Status: agentauth.StatusNotConfigured},
	}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"--agent", "claude-code", "auth", "status"},
		&stdout,
		&stderr,
		buildinfo.Info{},
		dependencies{
			detectAgent: func(override string) (agentidentity.Detection, error) {
				if override != "claude-code" {
					t.Fatalf("override = %q", override)
				}
				return agentidentity.Detection{
					Agent:  override,
					Source: "override",
				}, nil
			},
			newAuth: func(agent string) (authCommands, error) {
				selected = agent
				return auth, nil
			},
		},
	)
	if exitCode != 0 || stderr.Len() != 0 ||
		stdout.String() != "status: not-configured\n" ||
		selected != "claude-code" {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q selected=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
			selected,
		)
	}
}

func TestAgentAndIdentityListsAreDeterministic(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		args []string
		deps dependencies
		want string
	}{
		{
			args: []string{"agent", "list"},
			want: strings.Join(agentidentity.Known(), "\n") + "\n",
		},
		{
			args: []string{"auth", "identities"},
			deps: dependencies{
				listIdentities: func() ([]string, error) {
					return []string{"claude-code", "codex"}, nil
				},
			},
			want: "claude-code\ncodex\n",
		},
	} {
		var stdout, stderr bytes.Buffer
		exitCode := runWithDependencies(
			test.args,
			&stdout,
			&stderr,
			buildinfo.Info{},
			test.deps,
		)
		if exitCode != 0 || stderr.Len() != 0 || stdout.String() != test.want {
			t.Fatalf(
				"Run(%q) exit=%d stdout=%q stderr=%q",
				test.args,
				exitCode,
				stdout.String(),
				stderr.String(),
			)
		}
	}
}

func TestAuthCreateUsesExplicitCreationOperation(t *testing.T) {
	t.Parallel()

	auth := &fakeCreateAuthCommands{
		fakeAuthCommands: &fakeAuthCommands{},
		created: agentauth.CredentialStatus{
			Status:       agentauth.StatusActive,
			PrincipalID:  "agt_created",
			CredentialID: "acred_created",
			ClientID:     "clt_created",
		},
	}
	stdout, stderr, exitCode := runTestAuth(
		t,
		[]string{"auth", "create"},
		auth,
	)
	if exitCode != 0 || stderr != "" || auth.createCalls != 1 ||
		!strings.Contains(stdout, "principal: agt_created\n") {
		t.Fatalf(
			"create exit=%d stdout=%q stderr=%q calls=%d",
			exitCode,
			stdout,
			stderr,
			auth.createCalls,
		)
	}
}

func TestAuthStatusWithoutLocalCredentialIsUsefulAndDoesNotEnroll(t *testing.T) {
	t.Parallel()

	auth := &fakeAuthCommands{statusErr: agentauth.ErrCredentialNotFound}
	stdout, stderr, exitCode := runTestAuth(t, []string{"auth", "status"}, auth)
	if exitCode != 0 || stderr != "" || stdout != "status: not-configured\n" {
		t.Fatalf("status stdout=%q stderr=%q exit=%d", stdout, stderr, exitCode)
	}
	if auth.statusCalls != 1 || auth.revokeCalls != 0 {
		t.Fatalf("auth calls status=%d revoke=%d", auth.statusCalls, auth.revokeCalls)
	}
}

func TestAuthRevokePrintsConfirmedStateAndSupportsLocalNoOp(t *testing.T) {
	t.Parallel()

	for _, status := range []agentauth.CredentialStatus{
		{
			Status:       agentauth.StatusRevoked,
			PrincipalID:  "agt_current",
			CredentialID: "acred_current",
			ClientID:     "clt_current",
		},
		{Status: agentauth.StatusNotConfigured},
	} {
		auth := &fakeAuthCommands{revoked: status}
		stdout, stderr, exitCode := runTestAuth(t, []string{"auth", "revoke"}, auth)
		if exitCode != 0 || stderr != "" || !strings.HasPrefix(stdout, "status: "+status.Status) {
			t.Fatalf("revoke status=%#v stdout=%q stderr=%q exit=%d", status, stdout, stderr, exitCode)
		}
		if auth.statusCalls != 0 || auth.revokeCalls != 1 {
			t.Fatalf("auth calls status=%d revoke=%d", auth.statusCalls, auth.revokeCalls)
		}
		assertNoCredentialSecrets(t, stdout+stderr)
	}
}

func TestAuthFailuresRedactPrivateJWKTokensAndUnderlyingErrors(t *testing.T) {
	t.Parallel()

	privateJWK := `{"kty":"OKP","d":"PRIVATE-SEED","x":"public"}`
	reusableToken := "Bearer reusable-token-value"
	privateCause := errors.New(privateJWK + " " + reusableToken + " /private/keychain/path")
	for _, test := range []struct {
		name     string
		args     []string
		auth     *fakeAuthCommands
		wantCode string
		wantText string
	}{
		{
			name:     "status",
			args:     []string{"auth", "status"},
			auth:     &fakeAuthCommands{statusErr: privateCause},
			wantCode: "auth_status_failed",
			wantText: "could not read the selected agent identity status",
		},
		{
			name:     "revoke server",
			args:     []string{"auth", "revoke"},
			auth:     &fakeAuthCommands{revokeErr: privateCause},
			wantCode: "auth_revoke_failed",
			wantText: "could not revoke the selected agent identity",
		},
		{
			name: "revoke credential changed",
			args: []string{"auth", "revoke"},
			auth: &fakeAuthCommands{
				revokeErr: errors.Join(agentauth.ErrCredentialChanged, privateCause),
			},
			wantCode: "auth_credential_changed",
			wantText: "the prior agent identity was revoked, but the local credential changed; retry to revoke the selected identity",
		},
		{
			name: "revoke local cleanup",
			args: []string{"auth", "revoke"},
			auth: &fakeAuthCommands{
				revokeErr: errors.Join(agentauth.ErrCredentialCleanup, privateCause),
			},
			wantCode: "auth_local_cleanup_failed",
			wantText: "the installation was revoked, but its local credential could not be removed",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exitCode := runTestAuth(t, test.args, test.auth)
			if exitCode != diagnostic.ExitFailure || stdout != "" {
				t.Fatalf("stdout=%q stderr=%q exit=%d", stdout, stderr, exitCode)
			}
			if !strings.Contains(stderr, test.wantCode) ||
				!strings.Contains(stderr, test.wantText) {
				t.Fatalf("stderr = %q", stderr)
			}
			if strings.Contains(stderr, privateJWK) ||
				strings.Contains(stderr, "PRIVATE-SEED") ||
				strings.Contains(stderr, reusableToken) ||
				strings.Contains(stderr, "/private/keychain/path") {
				t.Fatalf("stderr leaked private material: %q", stderr)
			}
		})
	}
}

func TestAuthOutputRejectsUnexpectedOrUnsafeServerState(t *testing.T) {
	t.Parallel()

	privateSeed := "PRIVATE-SEED"
	auth := &fakeAuthCommands{
		status: agentauth.CredentialStatus{
			Status:       agentauth.StatusActive,
			PrincipalID:  "agt_safe",
			CredentialID: "acred_" + privateSeed + "\n",
			ClientID:     "clt_safe",
		},
	}
	stdout, stderr, exitCode := runTestAuth(t, []string{"auth", "status"}, auth)
	if exitCode != diagnostic.ExitFailure || stdout != "" {
		t.Fatalf("stdout=%q stderr=%q exit=%d", stdout, stderr, exitCode)
	}
	if strings.Contains(stderr, privateSeed) {
		t.Fatalf("stderr leaked rejected state: %q", stderr)
	}
}

func TestNonAuthAndInvalidAuthCommandsDoNotInitializeCredentialAccess(t *testing.T) {
	t.Parallel()

	dependencies := dependencies{
		newAuth: func(string) (authCommands, error) {
			t.Fatal("credential access initialized")
			return nil, nil
		},
	}
	for _, args := range [][]string{
		nil,
		{"help"},
		{"version"},
		{"auth"},
		{"auth", "unknown"},
		{"auth", "status", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		_ = runWithDependencies(args, &stdout, &stderr, buildinfo.Info{}, dependencies)
	}
}

func TestSearchStartUsesKadoAppContractOptions(t *testing.T) {
	t.Parallel()
	search := &fakeSearchCommands{response: completedAgentResponse()}
	var connection searchConnectionOptions
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"search", "--json", "--wait", "--timeout", "45s", "--poll-interval-ms", "500", "--base-url", "https://search.kado.test", "--api-key", "secret", "find", "agent", "tools"},
		&stdout, &stderr, buildinfo.Info{Version: "0.1.3"},
		dependencies{newSearch: func(_ string, options searchConnectionOptions) (searchCommands, error) {
			connection = options
			return search, nil
		}},
	)
	if exitCode != 0 || stderr.Len() != 0 || stdout.String() != string(search.response.ResultJSON) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if search.operation != "start" || search.start.Query != "find agent tools" || !search.start.Wait.Enabled || search.start.Wait.TimeoutMS != 45_000 || search.start.Wait.PollIntervalMS != 500 || search.start.Version != "0.1.3" {
		t.Fatalf("start=%#v operation=%q", search.start, search.operation)
	}
	if connection.baseURL != "https://search.kado.test" || connection.apiKey != "secret" {
		t.Fatalf("connection=%#v", connection)
	}
}

func TestSearchSubcommandsMapToAgentAPI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args      []string
		operation string
	}{
		{[]string{"search", "status", "app_search_1", "--json", "--wait"}, "status"},
		{[]string{"search", "refine", "app_search_1", "--dimension", "budget_monthly_usd=200", "--json"}, "refine"},
		{[]string{"search", "answer", "app_search_1", "lead_volume", "About 750 leads", "--json"}, "answer"},
		{[]string{"search", "cancel", "app_search_1", "--reason", "done", "--json"}, "cancel"},
	}
	for _, test := range tests {
		search := &fakeSearchCommands{response: completedAgentResponse()}
		var stdout, stderr bytes.Buffer
		exitCode := runWithDependencies(test.args, &stdout, &stderr, buildinfo.Info{}, dependencies{newSearch: func(string, searchConnectionOptions) (searchCommands, error) { return search, nil }})
		if exitCode != 0 || stderr.Len() != 0 || search.operation != test.operation {
			t.Fatalf("args=%v exit=%d operation=%q stderr=%q", test.args, exitCode, search.operation, stderr.String())
		}
	}
}

func TestSearchHumanOutputAndJSONErrorsStayBounded(t *testing.T) {
	t.Parallel()
	completed := &fakeSearchCommands{response: completedAgentResponse()}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies([]string{"search", "agent tools"}, &stdout, &stderr, buildinfo.Info{}, dependencies{newSearch: func(string, searchConnectionOptions) (searchCommands, error) { return completed, nil }})
	if exitCode != 0 || !strings.Contains(stdout.String(), "Search completed: app_search_1") || !strings.Contains(stdout.String(), "1. Kado Match") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}

	secret := "Bearer private-access-token /private/path"
	failed := &fakeSearchCommands{err: errors.New(secret)}
	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDependencies([]string{"search", "--json", "agent tools"}, &stdout, &stderr, buildinfo.Info{}, dependencies{newSearch: func(string, searchConnectionOptions) (searchCommands, error) { return failed, nil }})
	if exitCode != diagnostic.ExitFailure || stdout.Len() != 0 || strings.Contains(stderr.String(), secret) || !strings.Contains(stderr.String(), "search_failed") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}

	apiFailure := completedAgentResponse()
	apiFailure.ResultJSON = []byte("{\"schema_version\":\"agent-cli-json.v1\",\"state\":\"failed\"}\n")
	remote := &fakeSearchCommands{response: apiFailure, err: &agentapi.Error{Code: "agent.rate_limited", Message: "Try later.", StatusCode: 429}}
	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDependencies([]string{"search", "--json", "agent tools"}, &stdout, &stderr, buildinfo.Info{}, dependencies{newSearch: func(string, searchConnectionOptions) (searchCommands, error) { return remote, nil }})
	if exitCode != diagnostic.ExitFailure || stdout.String() != string(apiFailure.ResultJSON) || !strings.Contains(stderr.String(), "agent_rate_limited") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}

func TestSearchBrokenPipeStopsSilently(t *testing.T) {
	t.Parallel()
	search := &fakeSearchCommands{response: completedAgentResponse()}
	var stderr bytes.Buffer
	exitCode := runWithDependencies([]string{"search", "--json", "example query"}, closedPipeWriter{}, &stderr, buildinfo.Info{}, dependencies{newSearch: func(string, searchConnectionOptions) (searchCommands, error) { return search, nil }})
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("broken pipe exit=%d stderr=%q", exitCode, stderr.String())
	}
}

func TestInvalidSearchUsageDoesNotInitializeAuthentication(t *testing.T) {
	t.Parallel()
	dependencies := dependencies{newSearch: func(string, searchConnectionOptions) (searchCommands, error) {
		t.Fatal("Search authentication initialized")
		return nil, nil
	}}
	for _, args := range [][]string{
		{"search"}, {"search", "--unknown", "query"}, {"search", "--timeout", "forever", "query"},
		{"search", "answer", "missing"}, {"search", "refine", "app_search_1"},
		{"search", "cancel", "app_search_1", "--wait"}, {"search", "status", "bad/id"},
	} {
		var stdout, stderr bytes.Buffer
		if exitCode := runWithDependencies(args, &stdout, &stderr, buildinfo.Info{}, dependencies); exitCode != diagnostic.ExitUsage {
			t.Fatalf("Run(%q) exit=%d stderr=%q", args, exitCode, stderr.String())
		}
	}
}

func TestUpdateUsesVerifiedReleaseBoundaryAndDeterministicOutput(t *testing.T) {
	t.Parallel()

	releases := &fakeReleaseCommands{result: releaseclient.Result{
		FromVersion: "0.1.0",
		ToVersion:   "0.2.0",
		Target:      "linux/amd64",
		DryRun:      true,
	}}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"update", "--dry-run", "--allow-downgrade"},
		&stdout,
		&stderr,
		buildinfo.Info{Version: "0.1.0"},
		dependencies{newRelease: func(buildinfo.Info) (releaseCommands, error) {
			return releases, nil
		}},
	)
	if exitCode != 0 || stderr.Len() != 0 ||
		stdout.String() != "verified kado 0.2.0 for linux/amd64; no files changed\n" {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
	if !releases.options.DryRun || !releases.options.AllowDowngrade ||
		releases.options.CurrentVersion != "0.1.0" {
		t.Fatalf("update options = %#v", releases.options)
	}
}

func TestSkillInstallDefaultsToDetectedParentAndReportsOtherAgents(t *testing.T) {
	t.Parallel()

	skills := &fakeSkillCommands{installResult: skillclient.InstallResult{
		Version: "0.2.0",
		Installed: []skillclient.Installation{{
			Agent:   "codex",
			Path:    "/home/test/.codex/skills/kado-search",
			Version: "0.2.0",
		}},
		OtherAgents: []string{"claude-code"},
	}}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"skill", "install"},
		&stdout,
		&stderr,
		buildinfo.Info{},
		dependencies{
			detectAgent: func(string) (agentidentity.Detection, error) {
				return agentidentity.Detection{Agent: "codex", Source: "process"}, nil
			},
			newSkill: func(buildinfo.Info) (skillCommands, error) {
				return skills, nil
			},
		},
	)
	if exitCode != 0 || stderr.Len() != 0 ||
		skills.installOptions.CurrentAgent != "codex" ||
		!strings.Contains(stdout.String(), "installed kado-search 0.2.0 for codex") ||
		!strings.Contains(stdout.String(), "kado skill install --all") {
		t.Fatalf(
			"skill install exit=%d options=%#v stdout=%q stderr=%q",
			exitCode,
			skills.installOptions,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestUpdateDowngradeFailureIsSafe(t *testing.T) {
	t.Parallel()

	private := "private-signing-seed /private/release/path"
	releases := &fakeReleaseCommands{
		err: errors.Join(releaseclient.ErrDowngrade, errors.New(private)),
	}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"update"},
		&stdout,
		&stderr,
		buildinfo.Info{Version: "0.2.0"},
		dependencies{newRelease: func(buildinfo.Info) (releaseCommands, error) {
			return releases, nil
		}},
	)
	if exitCode != diagnostic.ExitFailure ||
		stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "release_downgrade_blocked") ||
		strings.Contains(stderr.String(), private) {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestUninstallPreservesCredentialsUnlessPurgeIsExplicit(t *testing.T) {
	t.Parallel()

	for _, purge := range []bool{false, true} {
		purge := purge
		t.Run(strconv.FormatBool(purge), func(t *testing.T) {
			t.Parallel()
			auth := &fakeAuthCommands{revoked: agentauth.CredentialStatus{
				Status: agentauth.StatusRevoked,
			}}
			releases := &fakeReleaseCommands{}
			args := []string{"uninstall", "--yes"}
			if purge {
				args = append(args, "--purge-credentials")
			}
			var stdout, stderr bytes.Buffer
			exitCode := runWithDependencies(
				args,
				&stdout,
				&stderr,
				buildinfo.Info{},
				dependencies{
					newAuth: func(string) (authCommands, error) {
						if !purge {
							t.Fatal("credential access initialized without purge")
						}
						return auth, nil
					},
					newRelease: func(buildinfo.Info) (releaseCommands, error) {
						return releases, nil
					},
				},
			)
			if exitCode != 0 || stderr.Len() != 0 || !releases.uninstalled {
				t.Fatalf(
					"exit=%d stdout=%q stderr=%q",
					exitCode,
					stdout.String(),
					stderr.String(),
				)
			}
			if purge != (auth.revokeCalls == 1) {
				t.Fatalf("purge=%t revokeCalls=%d", purge, auth.revokeCalls)
			}
			if !purge && !strings.Contains(stdout.String(), "credentials were preserved") {
				t.Fatalf("stdout = %q", stdout.String())
			}
		})
	}
}

func TestUninstallRequiresConfirmationBeforeAccessingAnything(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"uninstall"},
		&stdout,
		&stderr,
		buildinfo.Info{},
		dependencies{
			newAuth: func(string) (authCommands, error) {
				t.Fatal("credential access initialized")
				return nil, nil
			},
			newRelease: func(buildinfo.Info) (releaseCommands, error) {
				t.Fatal("release access initialized")
				return nil, nil
			},
		},
	)
	if exitCode != diagnostic.ExitUsage || stdout.Len() != 0 {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestReleaseVerifyUsesCandidateBundleBoundary(t *testing.T) {
	t.Parallel()

	releases := &fakeReleaseCommands{
		metadata: releaseclient.Metadata{Version: "0.1.0"},
		target: releaseclient.Target{
			OS:   "darwin",
			Arch: "arm64",
		},
	}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"release", "verify", "--directory", "/downloaded/release"},
		&stdout,
		&stderr,
		buildinfo.Info{},
		dependencies{newRelease: func(buildinfo.Info) (releaseCommands, error) {
			return releases, nil
		}},
	)
	if exitCode != 0 || stderr.Len() != 0 ||
		stdout.String() != "verified kado 0.1.0 for darwin/arm64\n" ||
		releases.directory != "/downloaded/release" {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q directory=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
			releases.directory,
		)
	}
}

type closedPipeWriter struct{}

func (closedPipeWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

type fakeAuthCommands struct {
	status      agentauth.CredentialStatus
	revoked     agentauth.CredentialStatus
	statusErr   error
	revokeErr   error
	statusCalls int
	revokeCalls int
}

type fakeCreateAuthCommands struct {
	*fakeAuthCommands
	created     agentauth.CredentialStatus
	createErr   error
	createCalls int
}

type fakeSearchCommands struct {
	response  agentapi.Response
	err       error
	operation string
	start     agentapi.StartRequest
	statusID  string
	refine    agentapi.RefineRequest
	answer    agentapi.AnswerRequest
	cancel    agentapi.CancelRequest
}

type fakeReleaseCommands struct {
	result       releaseclient.Result
	err          error
	options      releaseclient.Options
	uninstalled  bool
	uninstallErr error
	metadata     releaseclient.Metadata
	target       releaseclient.Target
	directory    string
	verifyErr    error
}

type fakeSkillCommands struct {
	installOptions skillclient.InstallOptions
	installResult  skillclient.InstallResult
}

func (commands *fakeSkillCommands) Install(
	_ context.Context,
	options skillclient.InstallOptions,
) (skillclient.InstallResult, error) {
	commands.installOptions = options
	return commands.installResult, nil
}

func (*fakeSkillCommands) Update(context.Context) (skillclient.UpdateResult, error) {
	return skillclient.UpdateResult{}, nil
}

func (*fakeSkillCommands) Status() (skillclient.Status, error) {
	return skillclient.Status{}, nil
}

func (*fakeSkillCommands) Uninstall(
	[]string,
	bool,
) ([]skillclient.Installation, error) {
	return nil, nil
}

func (releases *fakeReleaseCommands) Update(
	_ context.Context,
	options releaseclient.Options,
) (releaseclient.Result, error) {
	releases.options = options
	return releases.result, releases.err
}

func (releases *fakeReleaseCommands) Uninstall() error {
	releases.uninstalled = true
	return releases.uninstallErr
}

func (releases *fakeReleaseCommands) VerifyBundle(
	directory string,
) (releaseclient.Metadata, releaseclient.Target, error) {
	releases.directory = directory
	return releases.metadata, releases.target, releases.verifyErr
}

func (search *fakeSearchCommands) Start(_ context.Context, request agentapi.StartRequest) (agentapi.Response, error) {
	search.operation, search.start = "start", request
	return search.response, search.err
}
func (search *fakeSearchCommands) Status(_ context.Context, id string, _ agentapi.WaitOptions, _ agentapi.ResultLimits) (agentapi.Response, error) {
	search.operation, search.statusID = "status", id
	return search.response, search.err
}
func (search *fakeSearchCommands) Refine(_ context.Context, request agentapi.RefineRequest) (agentapi.Response, error) {
	search.operation, search.refine = "refine", request
	return search.response, search.err
}
func (search *fakeSearchCommands) Answer(_ context.Context, request agentapi.AnswerRequest) (agentapi.Response, error) {
	search.operation, search.answer = "answer", request
	return search.response, search.err
}
func (search *fakeSearchCommands) Cancel(_ context.Context, request agentapi.CancelRequest) (agentapi.Response, error) {
	search.operation, search.cancel = "cancel", request
	return search.response, search.err
}

func completedAgentResponse() agentapi.Response {
	searchID := "app_search_1"
	searchURL := "https://kado.so/search?q=agent+tools"
	result := agentapi.Result{
		SchemaVersion:  agentapi.JSONSchemaVersion,
		SearchID:       &searchID,
		SearchURL:      &searchURL,
		State:          "completed",
		BestMatches:    []agentapi.Match{{Rank: 1, SolutionID: "solution_1", Name: "Kado Match", Summary: "A strong match.", SolutionURL: "https://example.com", Why: []string{}, Constraints: agentapi.Constraints{Satisfied: []string{}, Violated: []string{}}, RequiredIntegrations: []string{}}},
		StretchMatches: []agentapi.Match{}, LaterMatches: []agentapi.Match{},
		Questions: []agentapi.Question{}, Dimensions: []agentapi.Dimension{},
	}
	return agentapi.Response{StatusCode: 200, Result: result, ResultJSON: []byte("{\"schema_version\":\"agent-cli-json.v1\",\"search_id\":\"app_search_1\",\"search_url\":\"https://kado.so/search?q=agent+tools\",\"state\":\"completed\",\"best_matches\":[],\"stretch_matches\":[],\"later_matches\":[],\"questions\":[],\"dimensions\":[],\"error\":null,\"pagination\":{},\"continuation\":{}}\n")}
}

func (auth *fakeAuthCommands) Status(
	context.Context,
) (agentauth.CredentialStatus, error) {
	auth.statusCalls++
	return auth.status, auth.statusErr
}

func (auth *fakeAuthCommands) Revoke(
	context.Context,
) (agentauth.CredentialStatus, error) {
	auth.revokeCalls++
	return auth.revoked, auth.revokeErr
}

func (auth *fakeCreateAuthCommands) Create(
	context.Context,
) (agentauth.CredentialStatus, error) {
	auth.createCalls++
	return auth.created, auth.createErr
}

func runTestAuth(
	t *testing.T,
	args []string,
	auth authCommands,
) (stdout, stderr string, exitCode int) {
	t.Helper()
	var stdoutBuffer, stderrBuffer bytes.Buffer
	exitCode = runWithDependencies(
		args,
		&stdoutBuffer,
		&stderrBuffer,
		buildinfo.Info{},
		dependencies{newAuth: func(string) (authCommands, error) {
			return auth, nil
		}},
	)
	return stdoutBuffer.String(), stderrBuffer.String(), exitCode
}

func unsafeTerminalRune(character rune) bool {
	return unicode.IsControl(character) && character != '\n' ||
		unicode.In(character, unicode.Cf, unicode.Zl, unicode.Zp)
}

func assertNoCredentialSecrets(t *testing.T, output string) {
	t.Helper()
	for _, forbidden := range []string{
		`"d"`,
		"PRIVATE",
		"Bearer ",
		"access_token",
		"client_assertion",
		"signature",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("ordinary output contains secret marker %q: %q", forbidden, output)
		}
	}
}
