package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/searchclient"
	"github.com/kado-so/search/internal/searchcontract"
	"github.com/kado-so/search/internal/searchoutput"
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
			wantText: "could not read current installation authentication status",
		},
		{
			name:     "revoke server",
			args:     []string{"auth", "revoke"},
			auth:     &fakeAuthCommands{revokeErr: privateCause},
			wantCode: "auth_revoke_failed",
			wantText: "could not revoke the current installation",
		},
		{
			name: "revoke credential changed",
			args: []string{"auth", "revoke"},
			auth: &fakeAuthCommands{
				revokeErr: errors.Join(agentauth.ErrCredentialChanged, privateCause),
			},
			wantCode: "auth_credential_changed",
			wantText: "the prior installation was revoked, but the local credential changed; retry to revoke the current installation",
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
		newAuth: func() (authCommands, error) {
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

func TestSearchRunsLifecycleWithBoundedOptionsAndSafeSummary(t *testing.T) {
	t.Parallel()

	canonical, err := searchcontract.ReleasedFixture("complete")
	if err != nil {
		t.Fatalf("ReleasedFixture(complete) error = %v", err)
	}
	search := &fakeSearchCommands{
		result: searchRunResult{
			status:    searchclient.StatusComplete,
			canonical: canonical,
			pages:     [][]byte{canonical},
		},
	}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{
			"search",
			"--timeout",
			"45s",
			"--answer",
			"Web",
			"--first-page",
			"--retry",
			"find",
			"agent",
			"tools",
		},
		&stdout,
		&stderr,
		buildinfo.Info{},
		dependencies{
			newSearch: func() (searchCommands, error) {
				return search, nil
			},
		},
	)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	expected, err := searchoutput.Render(
		canonical,
		[][]byte{canonical},
		searchoutput.Options{Mode: searchoutput.ModeHuman},
	)
	if err != nil {
		t.Fatalf("Render(expected human) error = %v", err)
	}
	if !bytes.Equal(stdout.Bytes(), expected) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if search.query != "find agent tools" ||
		search.options.Timeout != 45*time.Second ||
		search.options.FollowPages ||
		!search.options.RetryFailure ||
		search.options.Clarify == nil {
		t.Fatalf("query=%q options=%#v", search.query, search.options)
	}
	answer, err := search.options.Clarify(
		context.Background(),
		searchclient.Question{ID: "question_1"},
	)
	if err != nil || answer != "Web" {
		t.Fatalf("clarifier answer=%q error=%v", answer, err)
	}
}

func TestSearchFailuresAreBoundedAndNeverRenderPrivateCauses(t *testing.T) {
	t.Parallel()

	secret := "Bearer private-access-token /private/keychain/path"
	for _, test := range []struct {
		name     string
		err      error
		wantCode string
		wantText string
	}{
		{
			name:     "clarification",
			err:      &searchclient.NeedsInputError{},
			wantCode: "search_needs_input",
			wantText: "Search requires clarification",
		},
		{
			name: "structured failure",
			err: &searchclient.FailureError{Failure: searchclient.Failure{
				Code:      "source_unavailable",
				Message:   "A source was unavailable.",
				Retryable: true,
			}},
			wantCode: "source_unavailable",
			wantText: "A source was unavailable.",
		},
		{
			name:     "private cause",
			err:      errors.New(secret),
			wantCode: "search_failed",
			wantText: "Could not complete the Search",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			search := &fakeSearchCommands{err: test.err}
			var stdout, stderr bytes.Buffer
			exitCode := runWithDependencies(
				[]string{"search", "agent tools"},
				&stdout,
				&stderr,
				buildinfo.Info{},
				dependencies{
					newSearch: func() (searchCommands, error) {
						return search, nil
					},
				},
			)
			if exitCode != diagnostic.ExitFailure || stdout.Len() != 0 ||
				!strings.Contains(stderr.String(), test.wantCode) ||
				!strings.Contains(stderr.String(), test.wantText) ||
				strings.Contains(stderr.String(), secret) {
				t.Fatalf(
					"exit=%d stdout=%q stderr=%q",
					exitCode,
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
}

func TestSearchFailureStderrRemovesTerminalControlsAndPreservesUnicode(t *testing.T) {
	t.Parallel()

	search := &fakeSearchCommands{
		err: &searchclient.FailureError{Failure: searchclient.Failure{
			Code: "source_unavailable",
			Message: "before\u001b\u0085\u009b\u2028\u2029\u202e\u2066after " +
				"Café 世界 🧭",
			Retryable: true,
		}},
	}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"search", "agent tools"},
		&stdout,
		&stderr,
		buildinfo.Info{},
		dependencies{
			newSearch: func() (searchCommands, error) {
				return search, nil
			},
		},
	)
	if exitCode != diagnostic.ExitFailure ||
		stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "before after Café 世界 🧭") ||
		strings.ContainsFunc(stderr.String(), unsafeTerminalRune) {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestSearchOutputModesUseValidatedCanonicalBytesAndProjections(t *testing.T) {
	t.Parallel()

	canonical, err := searchcontract.ReleasedFixture("complete")
	if err != nil {
		t.Fatalf("ReleasedFixture(complete) error = %v", err)
	}
	for _, test := range []struct {
		name    string
		args    []string
		options searchoutput.Options
	}{
		{
			name:    "canonical JSON",
			args:    []string{"search", "--json", "example query"},
			options: searchoutput.Options{Mode: searchoutput.ModeJSON},
		},
		{
			name:    "JSONL",
			args:    []string{"search", "--jsonl", "example query"},
			options: searchoutput.Options{Mode: searchoutput.ModeJSONL},
		},
		{
			name:    "narrow human",
			args:    []string{"search", "--width", "52", "example query"},
			options: searchoutput.Options{Mode: searchoutput.ModeHuman, Width: 52},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			search := &fakeSearchCommands{result: searchRunResult{
				status:    searchclient.StatusComplete,
				canonical: canonical,
				pages:     [][]byte{canonical},
			}}
			var stdout, stderr bytes.Buffer
			exitCode := runWithDependencies(
				test.args,
				&stdout,
				&stderr,
				buildinfo.Info{},
				dependencies{newSearch: func() (searchCommands, error) {
					return search, nil
				}},
			)
			want, err := searchoutput.Render(canonical, [][]byte{canonical}, test.options)
			if err != nil {
				t.Fatalf("Render(expected %s) error = %v", test.name, err)
			}
			if exitCode != 0 ||
				stderr.Len() != 0 ||
				!bytes.Equal(stdout.Bytes(), want) {
				t.Fatalf(
					"%s exit=%d stdout=%q stderr=%q",
					test.name,
					exitCode,
					stdout.String(),
					stderr.String(),
				)
			}
			if test.options.Mode == searchoutput.ModeJSON && search.options.FollowPages {
				t.Fatal("--json followed pagination despite emitting one canonical document")
			}
		})
	}
}

func TestSearchFailureModesEmitValidatedLifecycleDocumentBeforeSafeDiagnostic(t *testing.T) {
	t.Parallel()

	canonical, err := searchcontract.ReleasedFixture("failed")
	if err != nil {
		t.Fatalf("ReleasedFixture(failed) error = %v", err)
	}
	search := &fakeSearchCommands{
		result: searchRunResult{
			status:    searchclient.StatusFailed,
			canonical: canonical,
		},
		err: &searchclient.FailureError{Failure: searchclient.Failure{
			Code:      "source_unavailable",
			Message:   "A required public source was unavailable.",
			Retryable: true,
		}},
	}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"search", "--json", "example query"},
		&stdout,
		&stderr,
		buildinfo.Info{},
		dependencies{newSearch: func() (searchCommands, error) {
			return search, nil
		}},
	)
	if exitCode != diagnostic.ExitFailure ||
		!bytes.Equal(stdout.Bytes(), canonical) ||
		!strings.Contains(stderr.String(), "source_unavailable") {
		t.Fatalf(
			"failure exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestSearchBrokenPipeStopsSilently(t *testing.T) {
	t.Parallel()

	canonical, err := searchcontract.ReleasedFixture("complete")
	if err != nil {
		t.Fatalf("ReleasedFixture(complete) error = %v", err)
	}
	search := &fakeSearchCommands{result: searchRunResult{
		status:    searchclient.StatusComplete,
		canonical: canonical,
		pages:     [][]byte{canonical},
	}}
	var stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"search", "--jsonl", "example query"},
		closedPipeWriter{},
		&stderr,
		buildinfo.Info{},
		dependencies{newSearch: func() (searchCommands, error) {
			return search, nil
		}},
	)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("broken pipe exit=%d stderr=%q", exitCode, stderr.String())
	}
}

func TestSearchUnsupportedMajorFailsClearlyBeforeOutput(t *testing.T) {
	t.Parallel()

	var value map[string]any
	if err := json.Unmarshal(mustReleasedFixture(t, "complete"), &value); err != nil {
		t.Fatalf("json.Unmarshal(complete) error = %v", err)
	}
	value["schema_version"] = "kado.search-document.v8"
	value["search"].(map[string]any)["query"] = "Bearer must-not-appear"
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(v8) error = %v", err)
	}
	search := &fakeSearchCommands{result: searchRunResult{
		status:    searchclient.StatusComplete,
		canonical: encoded,
		pages:     [][]byte{encoded},
	}}
	var stdout, stderr bytes.Buffer
	exitCode := runWithDependencies(
		[]string{"search", "--json", "example query"},
		&stdout,
		&stderr,
		buildinfo.Info{},
		dependencies{newSearch: func() (searchCommands, error) {
			return search, nil
		}},
	)
	if exitCode != diagnostic.ExitFailure ||
		stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "search_document_version_unsupported") ||
		!strings.Contains(stderr.String(), "v8") ||
		strings.Contains(stderr.String(), "Bearer") ||
		strings.Contains(stderr.String(), "must-not-appear") {
		t.Fatalf(
			"unsupported exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestInvalidSearchUsageDoesNotInitializeAuthentication(t *testing.T) {
	t.Parallel()

	dependencies := dependencies{
		newSearch: func() (searchCommands, error) {
			t.Fatal("Search authentication initialized")
			return nil, nil
		},
	}
	for _, args := range [][]string{
		{"search"},
		{"search", "--unknown", "query"},
		{"search", "--timeout", "forever", "query"},
		{"search", "--answer", "one", "--answer", "two", "query"},
		{"search", "--json", "--jsonl", "query"},
		{"search", "--width", "39", "query"},
		{"search", "--width", "161", "query"},
	} {
		var stdout, stderr bytes.Buffer
		exitCode := runWithDependencies(
			args,
			&stdout,
			&stderr,
			buildinfo.Info{},
			dependencies,
		)
		if exitCode != diagnostic.ExitUsage {
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
					newAuth: func() (authCommands, error) {
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
			newAuth: func() (authCommands, error) {
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

func mustReleasedFixture(t *testing.T, name string) []byte {
	t.Helper()
	value, err := searchcontract.ReleasedFixture(name)
	if err != nil {
		t.Fatalf("ReleasedFixture(%s) error = %v", name, err)
	}
	return value
}

type fakeAuthCommands struct {
	status      agentauth.CredentialStatus
	revoked     agentauth.CredentialStatus
	statusErr   error
	revokeErr   error
	statusCalls int
	revokeCalls int
}

type fakeSearchCommands struct {
	result  searchRunResult
	err     error
	query   string
	options searchclient.RunOptions
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

func (search *fakeSearchCommands) Run(
	_ context.Context,
	query string,
	options searchclient.RunOptions,
) (searchRunResult, error) {
	search.query = query
	search.options = options
	return search.result, search.err
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
		dependencies{newAuth: func() (authCommands, error) {
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
