package releaseclient

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/launcher"
	"github.com/kado-so/search/internal/payload"
)

// Deterministic private maintenance peer for signed transaction tests. The
// launcher native matrix independently exercises real multi-home MCP leases.
func TestMain(m *testing.M) {
	if len(os.Args) == 5 && os.Args[1] == "--no-global-search-paths" && strings.HasSuffix(filepath.ToSlash(os.Args[2]), "/maintenance/index.js") {
		r := bufio.NewScanner(os.Stdin)
		if !r.Scan() {
			os.Exit(2)
		}
		fmt.Println("{\"schema\":1,\"references\":[]}")
		if !r.Scan() || r.Text() != "release" {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func completeRelease(t *testing.T, version string, key ed25519.PrivateKey) *releaseFixture {
	t.Helper()
	f := newReleaseFixtureWithBinary(t, version, runtime.GOOS, runtime.GOARCH, key, nil)
	var metadata Metadata
	if err := json.Unmarshal(f.fetch[f.metadataURL], &metadata); err != nil {
		t.Fatal(err)
	}
	c := payload.Component{Version: "0.1.0-dev.0", Commit: strings.Repeat("a", 40), LockSHA256: strings.Repeat("b", 64), NodeVersion: "24.20.0"}
	metadata.SchemaVersion = CompleteSchemaVersion
	metadata.Components.MCP = &c
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	node, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	for i, target := range metadata.Targets {
		files := map[string][]byte{}
		entries := payload.Entries(target.OS + "/" + target.Arch)
		for role, p := range entries {
			files[p] = []byte(role + "-" + version)
		}
		files[entries["kado"]] = f.binary
		files[entries["a2a"]] = []byte("fixture-a2a-sidecar")
		if target.OS == runtime.GOOS && target.Arch == runtime.GOARCH {
			files[entries["node"]] = node
		}
		component := c
		component.NodeArchiveSHA256 = strings.Repeat("c", 64)
		b, err := payload.Seal(payload.Manifest{Version: version, Target: target.OS + "/" + target.Arch, MCP: component}, files, key)
		if err != nil {
			t.Fatal(err)
		}
		_, format, _ := targetLayout(target.OS)
		archive, err := payload.Archive(b, format, time.Unix(1700000000, 0).UTC())
		if err != nil {
			t.Fatal(err)
		}
		target.Archive.SHA256 = Digest(archive)
		target.Archive.Size = int64(len(archive))
		target.Payload = &EmbeddedArtifact{SHA256: Digest(b.Encoded), Size: int64(len(b.Encoded))}
		target.NodeArchiveSHA256 = component.NodeArchiveSHA256
		f.fetch[target.Archive.URL] = archive
		metadata.Targets[i] = target
		if target.OS == runtime.GOOS && target.Arch == runtime.GOARCH {
			f.target = target
		}
	}
	encoded, err := CanonicalMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := metadata.Validate(); err != nil {
		t.Fatal(err)
	}
	f.fetch[f.metadataURL] = encoded
	f.fetch[f.metadataURL+".sig"] = ed25519.Sign(key, encoded)
	return f
}

func TestSignedCompleteFreshUpdateRepairAndRollback(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, executableName(runtime.GOOS))
	first := completeRelease(t, "1.0.0", key)
	if _, err := first.manager(t).Install(context.Background(), Options{TargetPath: path}); err != nil {
		t.Fatal("fresh install", err)
	}
	second := completeRelease(t, "1.1.0", key)
	options := Options{TargetPath: path, LauncherPath: path, CurrentVersion: "1.0.0"}
	if _, err := second.manager(t).Update(context.Background(), options); err != nil {
		t.Fatal("update", err)
	}
	active, err := launcher.ActiveComplete(path, key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active.Entry("bridge"), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	options.CurrentVersion = "1.1.0"
	if _, err := second.manager(t).Update(context.Background(), options); err != nil {
		t.Fatal("repair", err)
	}
	if _, err := first.manager(t).Update(context.Background(), options); !errors.Is(err, ErrDowngrade) {
		t.Fatal("downgrade policy", err)
	}
	options.AllowDowngrade = true
	if _, err := first.manager(t).Update(context.Background(), options); err != nil {
		t.Fatal("rollback", err)
	}
	active, err = launcher.ActiveComplete(path, key.Public().(ed25519.PublicKey))
	if err != nil || active.Manifest.Version != "1.0.0" {
		t.Fatal("rollback did not activate complete old unit", err)
	}
}

func TestFreshCompleteInstallRejectsLegacyRelease(t *testing.T) {
	f := newReleaseFixture(t, "1.0.0", runtime.GOOS, runtime.GOARCH)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	path := filepath.Join(dir, executableName(runtime.GOOS))
	if _, err := f.manager(t).Install(context.Background(), Options{TargetPath: path}); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatal("fresh install accepted legacy metadata", err)
	}
	if _, _, err := launcher.ActiveBundle(path); err == nil {
		t.Fatal("fresh install created legacy activation")
	}
}

func TestSignedCompleteTamperNeverPublishes(t *testing.T) {
	for _, kind := range []string{"metadata", "signature", "archive", "manifest-descriptor", "node-identity"} {
		t.Run(kind, func(t *testing.T) {
			key := ed25519.NewKeyFromSeed(make([]byte, 32))
			f := completeRelease(t, "1.0.0", key)
			switch kind {
			case "metadata":
				f.fetch[f.metadataURL][0] ^= 1
			case "signature":
				f.fetch[f.metadataURL+".sig"][0] ^= 1
			case "archive":
				f.fetch[f.target.Archive.URL][10] ^= 1
			default:
				var m Metadata
				if err := json.Unmarshal(f.fetch[f.metadataURL], &m); err != nil {
					t.Fatal(err)
				}
				for i := range m.Targets {
					if kind == "manifest-descriptor" {
						m.Targets[i].Payload.SHA256 = strings.Repeat("d", 64)
					} else {
						m.Targets[i].NodeArchiveSHA256 = strings.Repeat("d", 64)
					}
				}
				b, _ := CanonicalMetadata(m)
				f.fetch[f.metadataURL] = b
				f.fetch[f.metadataURL+".sig"] = ed25519.Sign(key, b)
			}
			dir, _ := filepath.EvalSymlinks(t.TempDir())
			path := filepath.Join(dir, executableName(runtime.GOOS))
			if _, err := f.manager(t).Install(context.Background(), Options{TargetPath: path}); err == nil {
				t.Fatal("accepted tampered release")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("published launcher after failure", err)
			}
		})
	}
}

func TestCompleteCandidateVersionIdentity(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	f := completeRelease(t, "1.0.0", key)
	var metadata Metadata
	if err := json.Unmarshal(f.fetch[f.metadataURL], &metadata); err != nil {
		t.Fatal(err)
	}
	candidate := buildStampedExecutable(t, metadata, f.publicKey)
	if err := VerifyExecutable(context.Background(), candidate, metadata, f.target); err != nil {
		t.Fatal("complete stamped identity rejected", err)
	}
	wrong := f.target
	wrong.NodeArchiveSHA256 = strings.Repeat("d", 64)
	if err := VerifyExecutable(context.Background(), candidate, metadata, wrong); !errors.Is(err, ErrCandidate) {
		t.Fatal("accepted different private runtime identity", err)
	}
}

func TestSignedCompleteExecutableLaunchAndFallback(t *testing.T) {
	// This test owns short-lived executable trees. Background maintenance has
	// separate coverage and must not keep these executables open on Windows.
	t.Setenv("KADO_MAINTENANCE_CHILD", "1")
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	path := filepath.Join(dir, executableName(runtime.GOOS))
	for _, version := range []string{"1.0.0", "1.1.0"} {
		f := completeRelease(t, version, key)
		var metadata Metadata
		if err := json.Unmarshal(f.fetch[f.metadataURL], &metadata); err != nil {
			t.Fatal(err)
		}
		candidate := buildStampedExecutable(t, metadata, f.publicKey)
		value, err := os.ReadFile(candidate)
		if err != nil {
			t.Fatal(err)
		}
		b, err := VerifyCompleteTarget(f.target, f.fetch[f.target.Archive.URL], key.Public().(ed25519.PublicKey))
		if err != nil {
			t.Fatal(err)
		}
		b.Files[b.Manifest.Entries["kado"]] = value
		b, err = payload.Seal(b.Manifest, b.Files, key)
		if err != nil {
			t.Fatal(err)
		}
		_, format, _ := targetLayout(runtime.GOOS)
		archive, err := payload.Archive(b, format, time.Unix(1700000000, 0))
		if err != nil {
			t.Fatal(err)
		}
		f.binary = value
		f.target.Archive.SHA256 = Digest(archive)
		f.target.Archive.Size = int64(len(archive))
		f.target.Payload = &EmbeddedArtifact{SHA256: Digest(b.Encoded), Size: int64(len(b.Encoded))}
		f.fetch[f.target.Archive.URL] = archive
		for i, target := range metadata.Targets {
			if target.OS == runtime.GOOS && target.Arch == runtime.GOARCH {
				metadata.Targets[i] = f.target
			}
		}
		encoded, _ := CanonicalMetadata(metadata)
		f.fetch[f.metadataURL] = encoded
		f.fetch[f.metadataURL+".sig"] = ed25519.Sign(key, encoded)
		manager := f.manager(t)
		manager.VerifyCandidate = VerifyExecutable
		if version == "1.0.0" {
			_, err = manager.Install(context.Background(), Options{TargetPath: path})
		} else {
			_, err = manager.Update(context.Background(), Options{TargetPath: path, LauncherPath: path, CurrentVersion: "1.0.0"})
		}
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(path, "version", "--json").CombinedOutput()
		if err != nil {
			t.Fatalf("stable launcher: %v %s", err, out)
		}
		var report buildinfo.VersionReport
		if json.Unmarshal(out, &report) != nil || report.Kado.Version != version || report.SchemaVersion != buildinfo.CompleteVersionSchema {
			t.Fatalf("wrong active complete identity: %s", out)
		}
	}
	active, err := launcher.ActiveComplete(path, key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(active.Entry("maintenance")); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(path, "version", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("fallback: %v %s", err, out)
	}
	var report buildinfo.VersionReport
	if json.Unmarshal(out, &report) != nil || report.Kado.Version != "1.0.0" {
		t.Fatalf("fallback selected mixed bundle: %s", out)
	}
}
