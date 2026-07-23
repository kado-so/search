package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/agentauth"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/diagnostic"
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

type fakeAuthCommands struct {
	status      agentauth.CredentialStatus
	revoked     agentauth.CredentialStatus
	statusErr   error
	revokeErr   error
	statusCalls int
	revokeCalls int
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
