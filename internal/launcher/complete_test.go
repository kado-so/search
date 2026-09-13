package launcher

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/payload"
)

// Only the private stdio lease contract is faked in deterministic installer
// fault tests. TestCompleteNativeMCP exercises the real component and locks.
func TestMain(m *testing.M) {
	if len(os.Args) == 5 && os.Args[1] == "--no-global-search-paths" && strings.HasSuffix(filepath.ToSlash(os.Args[2]), "/maintenance/index.js") {
		r := bufio.NewScanner(os.Stdin)
		if !r.Scan() {
			os.Exit(2)
		}
		refs := os.Getenv("KADO_INSTALL_TEST_REFERENCES")
		if refs == "" {
			refs = "[]"
		}
		fmt.Printf("{\"schema\":1,\"references\":%s}\n", refs)
		if !r.Scan() || r.Text() != "release" {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func completeFixture(t *testing.T, version string) (payload.Bundle, ed25519.PublicKey) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{19}, 32))
	files := map[string][]byte{}
	for role, p := range payload.Entries(runtime.GOOS + "/" + runtime.GOARCH) {
		files[p] = []byte(role + "-" + version)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	node, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	files[payload.Entries(runtime.GOOS + "/" + runtime.GOARCH)["node"]] = node
	b, err := payload.Seal(payload.Manifest{Version: version, Target: runtime.GOOS + "/" + runtime.GOARCH, MCP: payload.Component{Version: "0.1.0-dev.0", Commit: strings.Repeat("a", 40), LockSHA256: strings.Repeat("b", 64), NodeVersion: "24.20.0", NodeArchiveSHA256: strings.Repeat("c", 64)}}, files, key)
	if err != nil {
		t.Fatal(err)
	}
	return b, key.Public().(ed25519.PublicKey)
}

func completeInstallation(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, executableName())
	if err := os.WriteFile(launcher, []byte("stable-launcher"), 0755); err != nil {
		t.Fatal(err)
	}
	return launcher
}

func installComplete(t *testing.T, launcher string, b payload.Bundle, key ed25519.PublicKey) {
	t.Helper()
	if err := WithUpdateLock(launcher, func() error { return InstallCompleteLocked(launcher, b, key) }); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteActivationFallbackRepairAndCollision(t *testing.T) {
	launcher := completeInstallation(t)
	one, key := completeFixture(t, "1.0.0")
	two, _ := completeFixture(t, "1.1.0")
	installComplete(t, launcher, one, key)
	installComplete(t, launcher, two, key)
	active, err := ActiveComplete(launcher, key)
	if err != nil || active.Manifest.Version != "1.1.0" {
		t.Fatalf("active: %v %v", active, err)
	}
	if err := os.WriteFile(active.Entry("maintenance"), []byte("tamper"), 0644); err != nil {
		t.Fatal(err)
	}
	fallback, err := ActiveComplete(launcher, key)
	if err != nil || fallback.Manifest.Version != "1.0.0" {
		t.Fatalf("fallback: %v %v", fallback, err)
	}
	installComplete(t, launcher, two, key)
	repaired, err := ActiveComplete(launcher, key)
	if err != nil || repaired.Digest != payload.Digest(two.Encoded) {
		t.Fatal("repair failed", err)
	}
	changed := two
	changed.Files = map[string][]byte{}
	for p, v := range two.Files {
		changed.Files[p] = v
	}
	changed.Files[changed.Manifest.Entries["mcp"]] = []byte("different bytes")
	changed, err = payload.Seal(changed.Manifest, changed.Files, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{19}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if err := WithUpdateLock(launcher, func() error { return InstallCompleteLocked(launcher, changed, key) }); err == nil {
		t.Fatal("accepted version collision")
	}
	stable, err := os.ReadFile(launcher)
	if err != nil || string(stable) != "stable-launcher" {
		t.Fatal("stable launcher changed")
	}
}

func TestCompleteRetentionPreservesReferencedVersion(t *testing.T) {
	launcher := completeInstallation(t)
	one, key := completeFixture(t, "1.0.0")
	installComplete(t, launcher, one, key)
	old, err := ActiveComplete(launcher, key)
	if err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(old.Root, "mcp", "app")
	refs, _ := json.Marshal([]payloadReference{{IPCVersion: 1, ComponentRoot: component, InstallationID: payload.Digest([]byte(component)), Runtime: old.Entry("node")}})
	t.Setenv("KADO_INSTALL_TEST_REFERENCES", string(refs))
	for _, version := range []string{"1.1.0", "1.2.0"} {
		b, _ := completeFixture(t, version)
		installComplete(t, launcher, b, key)
	}
	if _, err := os.Stat(old.Entry("node")); err != nil {
		t.Fatal("pruned referenced runtime", err)
	}
	t.Setenv("KADO_INSTALL_TEST_REFERENCES", "[]")
	newer, _ := completeFixture(t, "1.3.0")
	installComplete(t, launcher, newer, key)
	if _, err := os.Stat(old.Root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("unreferenced old version not pruned", err)
	}
	// Identity ledger survives pruning, so a reused version cannot change bytes.
	if _, err := os.Stat(filepath.Join(launcher+".d", "identities-v3", "1.0.0.json")); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRejectsMissingInventoryAndBusyRepair(t *testing.T) {
	launcher := completeInstallation(t)
	b, key := completeFixture(t, "1.0.0")
	installComplete(t, launcher, b, key)
	active, _ := ActiveComplete(launcher, key)
	component := filepath.Join(active.Root, "mcp", "app")
	refs, _ := json.Marshal([]payloadReference{{IPCVersion: 1, ComponentRoot: component, InstallationID: payload.Digest([]byte(component)), Runtime: active.Entry("node")}})
	t.Setenv("KADO_INSTALL_TEST_REFERENCES", string(refs))
	if err := os.WriteFile(active.Entry("mcp"), []byte("damaged"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WithUpdateLock(launcher, func() error { return InstallCompleteLocked(launcher, b, key) }); !errors.Is(err, ErrBusy) {
		t.Fatalf("repair must retain live version: %v", err)
	}
	if err := os.Remove(filepath.Join(launcher+".d", "registered-homes-v1.json")); err != nil {
		t.Fatal(err)
	}
	if err := WithUpdateLock(launcher, func() error { return InstallCompleteLocked(launcher, b, key) }); !errors.Is(err, ErrBusy) {
		t.Fatalf("missing inventory accepted: %v", err)
	}
}

func TestCompleteRejectsPackageReceiptAndPartialCandidate(t *testing.T) {
	launcher := completeInstallation(t)
	b, key := completeFixture(t, "1.0.0")
	if err := os.WriteFile(filepath.Join(filepath.Dir(launcher), receiptName), []byte("{\"schema_version\":1,\"channel\":\"scoop\"}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WithUpdateLock(launcher, func() error { return InstallCompleteLocked(launcher, b, key) }); err == nil {
		t.Fatal("accepted package-managed update")
	}
	if err := os.Remove(filepath.Join(filepath.Dir(launcher), receiptName)); err != nil {
		t.Fatal(err)
	}
	delete(b.Files, b.Manifest.Entries["bridge"])
	if err := WithUpdateLock(launcher, func() error { return InstallCompleteLocked(launcher, b, key) }); err == nil {
		t.Fatal("accepted partial candidate")
	}
	if _, err := ActiveComplete(launcher, key); err == nil {
		t.Fatal("published partial candidate")
	}
}

func TestCompleteInterruptedPublicationAndRecovery(t *testing.T) {
	for _, phase := range []string{"identities-v3", completeActivations} {
		t.Run(phase, func(t *testing.T) {
			launcher := completeInstallation(t)
			one, key := completeFixture(t, "1.0.0")
			installComplete(t, launcher, one, key)
			two, _ := completeFixture(t, "1.1.0")
			err := WithUpdateLock(launcher, func() error {
				return installCompleteWithRecords(launcher, two, key, func(p string, b []byte) error {
					if filepath.Base(filepath.Dir(p)) == phase {
						return &os.PathError{Op: "write", Path: p, Err: syscall.ENOSPC}
					}
					return atomicCompleteFile(p, b)
				})
			})
			if !errors.Is(err, syscall.ENOSPC) {
				t.Fatalf("expected injected storage failure: %v", err)
			}
			active, err := ActiveComplete(launcher, key)
			if err != nil || active.Manifest.Version != "1.0.0" {
				t.Fatal("failed publication changed active bundle", err)
			}
			installComplete(t, launcher, two, key)
			active, err = ActiveComplete(launcher, key)
			if err != nil || active.Manifest.Version != "1.1.0" {
				t.Fatal("recovery failed", err)
			}
		})
	}
}

func TestCompleteConcurrentReadersAndUpdaters(t *testing.T) {
	launcher := completeInstallation(t)
	one, key := completeFixture(t, "1.0.0")
	installComplete(t, launcher, one, key)
	two, _ := completeFixture(t, "1.1.0")
	three, _ := completeFixture(t, "1.2.0")
	var workers sync.WaitGroup
	failures := make(chan error, 3)
	done := make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			p, err := ActiveComplete(launcher, key)
			if err != nil {
				failures <- err
				return
			}
			if p.Manifest.Version != "1.0.0" && p.Manifest.Version != "1.1.0" && p.Manifest.Version != "1.2.0" {
				failures <- errors.New("mixed/unknown unit")
				return
			}
		}
	}()
	var updates sync.WaitGroup
	for _, b := range []payload.Bundle{two, three} {
		updates.Add(1)
		go func(b payload.Bundle) {
			defer updates.Done()
			if err := WithUpdateLock(launcher, func() error { return InstallCompleteLocked(launcher, b, key) }); err != nil {
				failures <- err
			}
		}(b)
	}
	updates.Wait()
	close(done)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestCompleteRemovalStopsWhenLeaseOwnerIsLost(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "payload")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("retain"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	checks := 0
	err := removePayloadTree(root, func() error {
		checks++
		if checks >= 6 {
			return ErrBusy
		}
		return nil
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatal("lost lease did not stop removal", err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal("cleanup continued after losing its lease", err)
		}
	}
}

func TestCompleteExecutableNeverBootstrapsLegacyPair(t *testing.T) {
	b, key := completeFixture(t, "1.0.0")
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := payload.WriteTree(root, b, key); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, b.Manifest.Entries["kado"])
	info := buildinfo.Info{Version: b.Manifest.Version, Target: b.Manifest.Target, MCP: &b.Manifest.MCP, InstallChannel: "direct", ReleasePublicKey: base64.RawStdEncoding.EncodeToString(key)}
	var output bytes.Buffer
	if code, handled := dispatchComplete(info, executable, []string{executable, "--help"}, nil, &output, &output, false); !handled || code != -1 {
		t.Fatalf("intact extracted unit: %d %t %s", code, handled, output.String())
	}
	if err := os.Remove(filepath.Join(root, b.Manifest.Entries["maintenance"])); err != nil {
		t.Fatal(err)
	}
	if code, handled := dispatchComplete(info, executable, []string{executable, "--help"}, nil, &output, &output, false); !handled || code != 1 {
		t.Fatal("incomplete successor fell through to legacy bootstrap")
	}
	if _, err := os.Stat(executable + ".d"); !os.IsNotExist(err) {
		t.Fatal("created legacy activation state")
	}
}
