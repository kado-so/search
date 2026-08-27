package main

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kado-so/search/internal/installchannel"
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
					`libexec.install "kado", "kado-a2a"`,
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
				if manifest.Bin != "kado.exe" || len(manifest.Architecture) != 2 {
					t.Fatalf("Scoop public surface = %#v", manifest)
				}
				if strings.Contains(readPackageFixture(t, output, "kado.json"), `"bin": "kado-a2a.exe"`) {
					t.Fatal("Scoop manifest exposes the private sidecar")
				}
			case installchannel.WinGet:
				installer := readPackageFixture(t, output, "manifests/Kado.Kado.installer.yaml")
				if strings.Count(installer, "RelativeFilePath: kado.exe") != 2 ||
					strings.Count(installer, "PortableCommandAlias: kado") != 2 ||
					strings.Contains(installer, "RelativeFilePath: kado-a2a.exe") {
					t.Fatalf("WinGet public surface is invalid: %s", installer)
				}
			case installchannel.Deb:
				text := readPackageFixture(t, output, "build-deb.sh")
				for _, want := range []string{"usr/libexec/kado/kado-a2a", "usr/bin/kado", "../libexec/kado/kado", "dpkg-deb --build"} {
					if !strings.Contains(text, want) {
						t.Fatalf("Debian definition does not contain %q: %s", want, text)
					}
				}
			case installchannel.RPM:
				for _, name := range []string{"kado-amd64.spec", "kado-arm64.spec"} {
					text := readPackageFixture(t, output, name)
					for _, want := range []string{"%{_libexecdir}/kado/kado-a2a", "%{_bindir}/kado", "../libexec/kado/kado"} {
						if !strings.Contains(text, want) {
							t.Fatalf("RPM definition %s does not contain %q: %s", name, want, text)
						}
					}
				}
			case installchannel.Container:
				text := readPackageFixture(t, output, "Dockerfile")
				if !strings.Contains(text, "ADD kado_1.2.3_linux_${TARGETARCH}.tar.gz /usr/local/libexec/kado/") ||
					!strings.Contains(text, `ENTRYPOINT ["/usr/local/libexec/kado/kado"]`) ||
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
	targets := targetsForInstallChannel(installchannel.Scoop)
	for _, target := range targets {
		if err := os.WriteFile(
			filepath.Join(kadoRoot, executableArtifactName("kado", "1.2.3", target)),
			[]byte("kado:"+target.goarch),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(a2aRoot, executableArtifactName("kado-a2a", "1.2.3", target)),
			[]byte("a2a:"+target.goarch),
			0o755,
		); err != nil {
			t.Fatal(err)
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
		"kado_1.2.3_windows_amd64.exe", "kado_1.2.3_windows_amd64.spdx.json", "provenance.intoto.json",
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
		"kado.json", "kado_1.2.3_windows_amd64.zip", "kado_1.2.3_windows_arm64.zip", "release-public-key.pem",
	} {
		if !strings.Contains(text, "  "+present+"\n") {
			t.Fatalf("checksums do not include %q: %s", present, text)
		}
	}
	if strings.Contains(text, "checksums.txt") {
		t.Fatalf("checksums include themselves: %s", text)
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
