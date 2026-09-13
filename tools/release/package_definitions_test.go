package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kado-so/search/internal/installchannel"
	"github.com/kado-so/search/internal/payload"
	"github.com/kado-so/search/internal/releaseclient"
)

func TestPackageDefinitionsPreservePrivateSiblingLayout(t *testing.T) {
	t.Parallel()

	for _, channel := range []string{
		installchannel.Homebrew,
		installchannel.WinGet,
		installchannel.Scoop,
		installchannel.Deb,
		installchannel.RPM,
		installchannel.Container,
	} {
		channel := channel
		t.Run(channel, func(t *testing.T) {
			t.Parallel()
			output := packageFixture(t)
			source := releaseIdentity{Version: "1.2.3", InstallURL: "https://kado.so/install"}
			if err := writePackageDefinitions(output, source, channel); err != nil {
				t.Fatal(err)
			}

			switch channel {
			case installchannel.Homebrew:
				text := readPackageFixture(t, output, "kado.rb")
				for _, want := range []string{
					`libexec.install Dir["*"]`,
					`bin.install_symlink libexec/"kado"`,
					"packages/homebrew/kado_1.2.3_darwin_arm64.tar.gz",
					"packages/homebrew/kado_1.2.3_linux_amd64.tar.gz",
				} {
					if !strings.Contains(text, want) {
						t.Fatalf("formula does not contain %q: %s", want, text)
					}
				}
			case installchannel.Scoop:
				var manifest struct {
					Architecture map[string]struct {
						URL  string `json:"url"`
						Hash string `json:"hash"`
					} `json:"architecture"`
					Bin string `json:"bin"`
				}
				if err := json.Unmarshal([]byte(readPackageFixture(t, output, "kado.json")), &manifest); err != nil {
					t.Fatal(err)
				}
				if manifest.Bin != "payload/kado.exe" || len(manifest.Architecture) != 2 {
					t.Fatalf("Scoop public surface = %#v", manifest)
				}
				if strings.Contains(readPackageFixture(t, output, "kado.json"), `"bin": "kado-a2a.exe"`) {
					t.Fatal("Scoop manifest exposes the private sidecar")
				}
			case installchannel.WinGet:
				installer := readPackageFixture(t, output, "manifests/Kado.Kado.installer.yaml")
				if strings.Count(installer, "RelativeFilePath: payload/kado.exe") != 2 ||
					strings.Count(installer, "PortableCommandAlias: kado") != 2 ||
					strings.Contains(installer, "RelativeFilePath: kado-a2a.exe") {
					t.Fatalf("WinGet public surface is invalid: %s", installer)
				}
			case installchannel.Deb:
				text := readPackageFixture(t, output, "build-deb.sh")
				for _, want := range []string{"tar -xzf \"$archive\" -C \"$work/root/usr/libexec/kado\"", "usr/bin/kado", "../libexec/kado/kado", "dpkg-deb --build"} {
					if !strings.Contains(text, want) {
						t.Fatalf("Debian definition does not contain %q: %s", want, text)
					}
				}
			case installchannel.RPM:
				for _, name := range []string{"kado-amd64.spec", "kado-arm64.spec"} {
					text := readPackageFixture(t, output, name)
					for _, want := range []string{"mcp bundle.gen.json bundle.gen.json.sig", "%{_bindir}/kado", "../libexec/kado/kado"} {
						if !strings.Contains(text, want) {
							t.Fatalf("RPM definition %s does not contain %q: %s", name, want, text)
						}
					}
				}
			case installchannel.Container:
				text := readPackageFixture(t, output, "Dockerfile")
				if !strings.Contains(text, "ADD kado_1.2.3_linux_${TARGETARCH}.tar.gz /usr/local/libexec/kado/") ||
					!strings.Contains(text, `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/libexec/kado/kado"]`) ||
					strings.Contains(text, `ENTRYPOINT ["/usr/local/libexec/kado/kado-a2a"]`) {
					t.Fatalf("container definition is invalid: %s", text)
				}
			}
		})
	}
}

func TestInstallChannelsBuildOnlyTheirSupportedTargets(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		installchannel.Direct:    {"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"},
		installchannel.Homebrew:  {"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"},
		installchannel.WinGet:    {"windows/amd64", "windows/arm64"},
		installchannel.Scoop:     {"windows/amd64", "windows/arm64"},
		installchannel.Deb:       {"linux/amd64", "linux/arm64"},
		installchannel.RPM:       {"linux/amd64", "linux/arm64"},
		installchannel.Container: {"linux/amd64", "linux/arm64"},
	}
	for channel, want := range tests {
		gotTargets := targetsForInstallChannel(channel)
		got := make([]string, 0, len(gotTargets))
		for _, target := range gotTargets {
			got = append(got, target.goos+"/"+target.goarch)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("targetsForInstallChannel(%q) = %v, want %v", channel, got, want)
		}
	}
	if got := targetsForInstallChannel("brew"); len(got) != 0 {
		t.Fatalf("targetsForInstallChannel(unknown) = %v", got)
	}
}

func TestPackageDefinitionsUseExactArchiveDigests(t *testing.T) {
	t.Parallel()

	output := packageFixture(t)
	if err := writePackageDefinitions(
		output,
		releaseIdentity{Version: "1.2.3", InstallURL: "https://kado.so/install/"},
		installchannel.Scoop,
	); err != nil {
		t.Fatal(err)
	}
	manifest := readPackageFixture(t, output, "kado.json")
	for _, arch := range []string{"amd64", "arm64"} {
		name := "kado_1.2.3_windows_" + arch + ".zip"
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(manifest, releaseclient.Digest(data)) {
			t.Fatalf("manifest does not contain digest for %s", name)
		}
	}
}

func TestPackageReleaseHasASeparateSignedArtifactBoundary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte("Kado license\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kadoRoot := t.TempDir()
	a2aRoot := t.TempDir()
	mcpRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	digests := map[string]string{}
	targets := targetsForInstallChannel(installchannel.Scoop)
	for _, target := range targets {
		dir, digest, _ := releaseComponentFixture(t, target)
		if err := os.Rename(dir, filepath.Join(mcpRoot, target.goos+"-"+target.goarch)); err != nil {
			t.Fatal(err)
		}
		digests[target.goos+"/"+target.goarch] = digest
		for _, binary := range []struct{ name, module, output string }{{"kado", "github.com/kado-so/search", kadoRoot}, {"kado-a2a", a2aModule, a2aRoot}} {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+binary.module+"\n\ngo 1.26.0\n\ntoolchain go1.26.4\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
				t.Fatal(err)
			}
			c := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(binary.output, executableArtifactName(binary.name, "1.2.3", target)), ".")
			c.Dir = dir
			c.Env = append(os.Environ(), "GOOS="+target.goos, "GOARCH="+target.goarch, "CGO_ENABLED=0")
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v %s", err, out)
			}
		}
	}
	private := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	public := private.Public().(ed25519.PublicKey)
	output := t.TempDir()
	if err := buildPackageRelease(buildInput{
		root:         root,
		output:       output,
		kadoPrebuilt: kadoRoot,
		a2aPrebuilt:  a2aRoot,
		a2aLicense:   []byte("A2A license\n"),
		mcpPrebuilt:  mcpRoot, mcpCommit: strings.Repeat("a", 40), mcpDigests: digests,
		a2a: a2aPreparedSource{Lock: a2aSourceLock{Version: "0.1.0", Repository: a2aRepository, License: a2aLicenseLock{SPDX: "Apache-2.0"}}},
		source: releaseIdentity{
			Version: "1.2.3", InstallURL: "https://kado.so/install", Repository: "https://github.com/kado-so/search",
		},
		builtAt:    time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
		privateKey: private,
		publicKey:  public,
		publicPEM:  []byte("public key fixture\n"),
		keyID:      "sha256:fixture",
		channel:    installchannel.Scoop,
		targets:    targets,
	}); err != nil {
		t.Fatal(err)
	}

	for _, absent := range []string{
		"release-metadata.json", "release-metadata.json.sig", "install.ps1", "install.sh",
	} {
		if _, err := os.Stat(filepath.Join(output, absent)); !os.IsNotExist(err) {
			t.Fatalf("package release contains direct artifact %q: %v", absent, err)
		}
	}
	checksums, err := os.ReadFile(filepath.Join(output, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(output, "checksums.txt.sig"))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(public, checksums, signature) {
		t.Fatal("package checksum signature is invalid")
	}
	text := string(checksums)
	for _, present := range []string{
		"kado_1.2.3_windows_amd64.spdx.json", "provenance.intoto.json", "kado.json", "kado_1.2.3_windows_amd64.zip", "kado_1.2.3_windows_arm64.zip", "release-public-key.pem",
	} {
		if !strings.Contains(text, "  "+present+"\n") {
			t.Fatalf("checksums do not include %q: %s", present, text)
		}
	}
	if strings.Contains(text, "checksums.txt") {
		t.Fatalf("checksums include themselves: %s", text)
	}
	for _, target := range targets {
		archive, err := os.ReadFile(filepath.Join(output, "kado_1.2.3_windows_"+target.goarch+".zip"))
		if err != nil {
			t.Fatal(err)
		}
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatal(err)
		}
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range reader.File {
			if !strings.HasPrefix(file.Name, "payload/") || !payload.ValidPath(file.Name) || !file.Mode().IsRegular() {
				t.Fatalf("unexpected package entry: %s", file.Name)
			}
			r, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			value, err := io.ReadAll(r)
			if closeErr := r.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, filepath.FromSlash(file.Name))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, value, file.Mode().Perm()); err != nil {
				t.Fatal(err)
			}
		}
		// Scoop/WinGet own the outer directory; their files are not signed code.
		if err := os.WriteFile(filepath.Join(root, "install.json"), []byte("manager metadata"), 0600); err != nil {
			t.Fatal(err)
		}
		bundleRoot := filepath.Join(root, "payload")
		encoded, err := os.ReadFile(filepath.Join(bundleRoot, payload.ManifestName))
		if err != nil {
			t.Fatal(err)
		}
		m, err := payload.VerifyTree(bundleRoot, payload.Digest(encoded), public)
		if err != nil || m.Target != "windows/"+target.goarch {
			t.Fatalf("package verification: %+v %v", m, err)
		}
		if got := readPackageFixture(t, bundleRoot, "kado.install.json"); !strings.Contains(got, `"channel":"scoop"`) {
			t.Fatalf("owner receipt: %s", got)
		}
		if err := os.WriteFile(filepath.Join(bundleRoot, "unlisted.js"), []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := payload.VerifyTree(bundleRoot, payload.Digest(encoded), public); err == nil {
			t.Fatal("package accepted code outside its signed inventory")
		}
	}
}

func TestPackageDefinitionsRejectDirectAndUnknownChannels(t *testing.T) {
	t.Parallel()

	for _, channel := range []string{installchannel.Direct, "brew", ""} {
		if err := writePackageDefinitions(t.TempDir(), releaseIdentity{}, channel); err == nil {
			t.Fatalf("writePackageDefinitions(%q) succeeded", channel)
		}
	}
}

func TestRPMSemanticPrereleaseHasComparablePackageIdentity(t *testing.T) {
	t.Parallel()

	version, release := rpmIdentity("1.2.3-rc-1+build.7")
	if version != "1.2.3" || release != "0.rc.1.1%{?dist}" {
		t.Fatalf("rpmIdentity() = %q, %q", version, release)
	}
	version, release = rpmIdentity("1.2.3")
	if version != "1.2.3" || release != "1%{?dist}" {
		t.Fatalf("rpmIdentity(stable) = %q, %q", version, release)
	}
}

func packageFixture(t *testing.T) string {
	t.Helper()
	output := t.TempDir()
	for _, target := range releaseTargets {
		extension := ".tar.gz"
		if target.goos == "windows" {
			extension = ".zip"
		}
		name := "kado_1.2.3_" + target.goos + "_" + target.goarch + extension
		if err := os.WriteFile(filepath.Join(output, name), []byte("pair:"+target.goos+"/"+target.goarch), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return output
}

func readPackageFixture(t *testing.T, root, name string) string {
	t.Helper()
	value, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}
