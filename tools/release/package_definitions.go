package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kado-so/search/internal/installchannel"
	"github.com/kado-so/search/internal/releaseclient"
)

type packageArchive struct {
	name   string
	url    string
	sha256 string
}

func targetsForInstallChannel(channel string) []buildTarget {
	match := func(target buildTarget) bool {
		switch channel {
		case installchannel.Direct:
			return true
		case installchannel.Homebrew:
			return target.goos == "darwin" || target.goos == "linux"
		case installchannel.WinGet, installchannel.Scoop:
			return target.goos == "windows"
		case installchannel.Deb, installchannel.RPM, installchannel.Container:
			return target.goos == "linux"
		default:
			return false
		}
	}
	targets := make([]buildTarget, 0, len(releaseTargets))
	for _, target := range releaseTargets {
		if match(target) {
			targets = append(targets, target)
		}
	}
	return targets
}

func buildPackageRelease(input buildInput) error {
	if !installchannel.Valid(input.channel) || input.channel == installchannel.Direct {
		return errors.New("package release channel is invalid")
	}
	license, err := os.ReadFile(filepath.Join(input.root, "LICENSE"))
	if err != nil {
		return errors.New("release license is unavailable")
	}
	guide := []byte(installGuide(input.source, input.keyID))
	for _, target := range input.targets {
		kadoName := executableArtifactName("kado", input.source.Version, target)
		kadoBinary, err := os.ReadFile(filepath.Join(input.kadoPrebuilt, kadoName))
		if err != nil || len(kadoBinary) == 0 {
			return fmt.Errorf("Kado binary is unavailable for %s/%s", target.goos, target.goarch)
		}
		a2aName := executableArtifactName("kado-a2a", input.source.Version, target)
		a2aBinary, err := os.ReadFile(filepath.Join(input.a2aPrebuilt, a2aName))
		if err != nil || len(a2aBinary) == 0 {
			return fmt.Errorf("A2A binary is unavailable for %s/%s", target.goos, target.goarch)
		}
		binaryName := "kado"
		archiveName := fmt.Sprintf("kado_%s_%s_%s.tar.gz", input.source.Version, target.goos, target.goarch)
		archiveFormat := "tar.gz"
		var archive []byte
		if target.goos == "windows" {
			binaryName = "kado.exe"
			archiveName = fmt.Sprintf("kado_%s_%s_%s.zip", input.source.Version, target.goos, target.goarch)
			archiveFormat = "zip"
			archive, err = makeZip(input.builtAt, binaryName, kadoBinary, a2aBinary, license, input.a2aLicense, guide)
		} else {
			archive, err = makeTarGzip(input.builtAt, binaryName, kadoBinary, a2aBinary, license, input.a2aLicense, guide)
		}
		if err != nil {
			return err
		}
		extracted, err := releaseclient.ExtractBundle(archive, archiveFormat, binaryName)
		if err != nil || !bytes.Equal(extracted.Kado, kadoBinary) || !bytes.Equal(extracted.A2A, a2aBinary) {
			return fmt.Errorf("package archive self-check failed for %s/%s", target.goos, target.goarch)
		}
		if err := writeReleaseArtifact(input.output, archiveName, archive, 0o644); err != nil {
			return err
		}
	}
	if err := writePackageDefinitions(input.output, input.source, input.channel); err != nil {
		return err
	}
	if err := writeReleaseArtifact(input.output, "release-public-key.pem", input.publicPEM, 0o644); err != nil {
		return err
	}
	checksums, err := packageChecksums(input.output)
	if err != nil {
		return err
	}
	if err := writeReleaseArtifact(input.output, "checksums.txt", checksums, 0o644); err != nil {
		return err
	}
	signature := ed25519.Sign(input.privateKey, checksums)
	if err := writeReleaseArtifact(input.output, "checksums.txt.sig", signature, 0o644); err != nil {
		return err
	}
	if !ed25519.Verify(input.publicKey, checksums, signature) {
		return errors.New("package checksum signature self-check failed")
	}
	return nil
}

func packageChecksums(root string) ([]byte, error) {
	type entry struct {
		name   string
		digest string
	}
	entries := make([]entry, 0)
	err := filepath.WalkDir(root, func(path string, value os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if value.IsDir() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if name == "checksums.txt" || name == "checksums.txt.sig" {
			return errors.New("package checksum output already exists")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{name: name, digest: releaseclient.Digest(data)})
		return nil
	})
	if err != nil {
		return nil, errors.New("package artifacts could not be checksummed")
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].name < entries[right].name })
	var output strings.Builder
	for _, value := range entries {
		fmt.Fprintf(&output, "%s  %s\n", value.digest, value.name)
	}
	return []byte(output.String()), nil
}

func writePackageDefinitions(output string, source releaseIdentity, channel string) error {
	if !installchannel.Valid(channel) || channel == installchannel.Direct {
		return errors.New("package definition channel is invalid")
	}
	assetBase := strings.TrimSuffix(source.InstallURL, "/") +
		"/releases/" + source.Version + "/packages/" + channel
	archive := func(goos, goarch string) (packageArchive, error) {
		extension := ".tar.gz"
		if goos == "windows" {
			extension = ".zip"
		}
		name := fmt.Sprintf("kado_%s_%s_%s%s", source.Version, goos, goarch, extension)
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil || len(data) == 0 {
			return packageArchive{}, fmt.Errorf("package archive is unavailable for %s/%s", goos, goarch)
		}
		return packageArchive{name: name, url: assetBase + "/" + name, sha256: releaseclient.Digest(data)}, nil
	}
	write := func(name, value string, mode fs.FileMode) error {
		return writeReleaseArtifact(output, name, []byte(value), mode)
	}

	switch channel {
	case installchannel.Homebrew:
		archives, err := packageArchives(archive, []buildTarget{
			{goos: "darwin", goarch: "amd64"},
			{goos: "darwin", goarch: "arm64"},
			{goos: "linux", goarch: "amd64"},
			{goos: "linux", goarch: "arm64"},
		})
		if err != nil {
			return err
		}
		return write("kado.rb", homebrewFormula(source.Version, archives), 0o644)
	case installchannel.Scoop:
		amd64, err := archive("windows", "amd64")
		if err != nil {
			return err
		}
		arm64, err := archive("windows", "arm64")
		if err != nil {
			return err
		}
		manifest, err := scoopManifest(source.Version, amd64, arm64)
		if err != nil {
			return err
		}
		return write("kado.json", manifest, 0o644)
	case installchannel.WinGet:
		amd64, err := archive("windows", "amd64")
		if err != nil {
			return err
		}
		arm64, err := archive("windows", "arm64")
		if err != nil {
			return err
		}
		for name, value := range wingetManifests(source.Version, amd64, arm64) {
			if err := write(name, value, 0o644); err != nil {
				return err
			}
		}
		return nil
	case installchannel.Deb:
		if _, err := archive("linux", "amd64"); err != nil {
			return err
		}
		if _, err := archive("linux", "arm64"); err != nil {
			return err
		}
		return write("build-deb.sh", debBuildScript(source.Version), 0o755)
	case installchannel.RPM:
		amd64, err := archive("linux", "amd64")
		if err != nil {
			return err
		}
		arm64, err := archive("linux", "arm64")
		if err != nil {
			return err
		}
		if err := write("kado-amd64.spec", rpmSpec(source.Version, "x86_64", amd64.name), 0o644); err != nil {
			return err
		}
		return write("kado-arm64.spec", rpmSpec(source.Version, "aarch64", arm64.name), 0o644)
	case installchannel.Container:
		if _, err := archive("linux", "amd64"); err != nil {
			return err
		}
		if _, err := archive("linux", "arm64"); err != nil {
			return err
		}
		return write("Dockerfile", containerDockerfile(source.Version), 0o644)
	default:
		return errors.New("package definition channel is invalid")
	}
}

func packageArchives(
	read func(string, string) (packageArchive, error),
	targets []buildTarget,
) (map[string]packageArchive, error) {
	archives := make(map[string]packageArchive, len(targets))
	for _, target := range targets {
		value, err := read(target.goos, target.goarch)
		if err != nil {
			return nil, err
		}
		archives[target.goos+"/"+target.goarch] = value
	}
	return archives, nil
}

func homebrewFormula(version string, archives map[string]packageArchive) string {
	entry := func(target string) string {
		value := archives[target]
		return fmt.Sprintf("    url %q\n    sha256 %q\n", value.url, value.sha256)
	}
	return fmt.Sprintf(`class Kado < Formula
  desc "Find and invoke agent solutions"
  homepage "https://kado.so"
  version %q
  license any_of: ["LicenseRef-Kado-Proprietary", "Apache-2.0"]

  on_macos do
    if Hardware::CPU.arm?
%s    else
%s    end
  end

  on_linux do
    if Hardware::CPU.arm?
%s    else
%s    end
  end

  def install
    libexec.install "kado", "kado-a2a"
    bin.install_symlink libexec/"kado"
  end
end
`, version, entry("darwin/arm64"), entry("darwin/amd64"), entry("linux/arm64"), entry("linux/amd64"))
}

func scoopManifest(version string, amd64, arm64 packageArchive) (string, error) {
	manifest := struct {
		Version      string `json:"version"`
		Description  string `json:"description"`
		Homepage     string `json:"homepage"`
		License      string `json:"license"`
		Architecture map[string]struct {
			URL  string `json:"url"`
			Hash string `json:"hash"`
		} `json:"architecture"`
		Bin string `json:"bin"`
	}{
		Version: version, Description: "Find and invoke agent solutions", Homepage: "https://kado.so",
		License: "Proprietary, Apache-2.0", Bin: "kado.exe",
		Architecture: map[string]struct {
			URL  string `json:"url"`
			Hash string `json:"hash"`
		}{
			"64bit": {URL: amd64.url, Hash: amd64.sha256},
			"arm64": {URL: arm64.url, Hash: arm64.sha256},
		},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", errors.New("Scoop manifest could not be encoded")
	}
	return string(encoded) + "\n", nil
}

func wingetManifests(version string, amd64, arm64 packageArchive) map[string]string {
	return map[string]string{
		"manifests/Kado.Kado.yaml": fmt.Sprintf(`# yaml-language-server: $schema=https://aka.ms/winget-manifest.version.1.12.0.schema.json
PackageIdentifier: Kado.Kado
PackageVersion: %s
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.12.0
`, version),
		"manifests/Kado.Kado.locale.en-US.yaml": fmt.Sprintf(`# yaml-language-server: $schema=https://aka.ms/winget-manifest.defaultLocale.1.12.0.schema.json
PackageIdentifier: Kado.Kado
PackageVersion: %s
PackageLocale: en-US
Publisher: Kado
PackageName: Kado
License: Proprietary, Apache-2.0
ShortDescription: Find and invoke agent solutions
ManifestType: defaultLocale
ManifestVersion: 1.12.0
`, version),
		"manifests/Kado.Kado.installer.yaml": fmt.Sprintf(`# yaml-language-server: $schema=https://aka.ms/winget-manifest.installer.1.12.0.schema.json
PackageIdentifier: Kado.Kado
PackageVersion: %s
InstallerType: zip
NestedInstallerType: portable
UpgradeBehavior: uninstallPrevious
Installers:
  - Architecture: x64
    InstallerUrl: %s
    InstallerSha256: %s
    NestedInstallerFiles:
      - RelativeFilePath: kado.exe
        PortableCommandAlias: kado
  - Architecture: arm64
    InstallerUrl: %s
    InstallerSha256: %s
    NestedInstallerFiles:
      - RelativeFilePath: kado.exe
        PortableCommandAlias: kado
ManifestType: installer
ManifestVersion: 1.12.0
`, version, amd64.url, strings.ToUpper(amd64.sha256), arm64.url, strings.ToUpper(arm64.sha256)),
	}
}

func debBuildScript(version string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu

test "$#" -eq 1 || { printf 'usage: build-deb.sh <amd64|arm64>\n' >&2; exit 2; }
arch="$1"
case "$arch" in amd64|arm64) ;; *) printf 'unsupported architecture\n' >&2; exit 2 ;; esac
here="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
archive="$here/kado_%s_linux_${arch}.tar.gz"
work="$(mktemp -d "${TMPDIR:-/tmp}/kado-deb.XXXXXX")"
cleanup() { rm -rf "$work"; }
trap cleanup EXIT HUP INT TERM
mkdir -p "$work/root/DEBIAN" "$work/root/usr/libexec/kado" "$work/root/usr/bin"
tar -xzf "$archive" -C "$work" kado kado-a2a
install -m 755 "$work/kado" "$work/root/usr/libexec/kado/kado"
install -m 755 "$work/kado-a2a" "$work/root/usr/libexec/kado/kado-a2a"
ln -s ../libexec/kado/kado "$work/root/usr/bin/kado"
printf 'Package: kado\nVersion: %s\nArchitecture: %%s\nMaintainer: Kado <support@kado.so>\nDescription: Find and invoke agent solutions\n' "$arch" >"$work/root/DEBIAN/control"
dpkg-deb --build --root-owner-group "$work/root" "$here/kado_%s_${arch}.deb"
`, version, version, version)
}

func rpmSpec(version, rpmArch, archiveName string) string {
	rpmVersion, rpmRelease := rpmIdentity(version)
	return fmt.Sprintf(`Name: kado
Version: %s
Release: %s
Summary: Find and invoke agent solutions
License: LicenseRef-Kado-Proprietary AND Apache-2.0
URL: https://kado.so
Source0: %s
BuildArch: %s

%%description
Kado finds and invokes agent solutions.

%%prep
%%setup -q -c -T
tar -xzf %%{SOURCE0}

%%install
install -d %%{buildroot}%%{_libexecdir}/kado %%{buildroot}%%{_bindir}
install -m 0755 kado %%{buildroot}%%{_libexecdir}/kado/kado
install -m 0755 kado-a2a %%{buildroot}%%{_libexecdir}/kado/kado-a2a
ln -s ../libexec/kado/kado %%{buildroot}%%{_bindir}/kado

%%files
%%{_bindir}/kado
%%{_libexecdir}/kado/kado
%%{_libexecdir}/kado/kado-a2a
`, rpmVersion, rpmRelease, archiveName, rpmArch)
}

func rpmIdentity(version string) (string, string) {
	withoutBuild := strings.SplitN(version, "+", 2)[0]
	parts := strings.SplitN(withoutBuild, "-", 2)
	if len(parts) == 1 {
		return parts[0], "1%{?dist}"
	}
	prerelease := strings.NewReplacer("-", ".", "_", ".").Replace(parts[1])
	return parts[0], "0." + prerelease + ".1%{?dist}"
}

func containerDockerfile(version string) string {
	return fmt.Sprintf(`FROM scratch
ARG TARGETARCH
ADD kado_%s_linux_${TARGETARCH}.tar.gz /usr/local/libexec/kado/
ENTRYPOINT ["/usr/local/libexec/kado/kado"]
`, version)
}
