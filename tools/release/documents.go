package main

import "fmt"

func installGuide(source releaseConfig, keyID string) string {
	return fmt.Sprintf(`# Install the Kado CLI

Version: %s
Source: %s
Install metadata: %s
Release signing key: %s

Download the complete release directory for your platform through a browser,
package manager, or another HTTPS client. Do not pipe a network response into a
shell. Keep release-metadata.json, release-metadata.json.sig,
release-public-key.pem, checksums.txt, and the platform archive together.

On Linux or macOS, inspect and run:

    sh install.sh . "$HOME/.local/bin/kado"

On Windows, inspect and run:

    powershell -NoProfile -File .\install.ps1 -ReleaseDirectory . -Destination "$env:LOCALAPPDATA\Kado\kado.exe"

The installers authenticate the canonical metadata, verify the selected
archive checksum, inspect the archive paths and executable mode, verify the
candidate's stamped version/provenance, and install to an empty destination
atomically. They refuse to overwrite an existing installation; use kado update
so signed update and downgrade policy cannot be bypassed. During an
update, the existing binary is retained as a rollback file until candidate
verification and replacement complete.

After installation:

    kado version --json
    kado auth status

Use kado update for a signed in-place update. Downgrades are rejected unless
--allow-downgrade is explicit. Use the supplied uninstall script with --yes;
credentials are preserved unless --purge-credentials is also explicit.
`, source.Version, source.Repository, source.InstallURL, keyID)
}

func installUnixScript(source releaseConfig, keyID string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu

release_directory="${1:-.}"
destination="${2:-${HOME}/.local/bin/kado}"
version=%q
expected_key_id=%q

case "$(uname -s)" in
  Darwin) target_os=darwin ;;
  Linux) target_os=linux ;;
  *) printf 'unsupported operating system\n' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) target_arch=amd64 ;;
  arm64|aarch64) target_arch=arm64 ;;
  *) printf 'unsupported architecture\n' >&2; exit 1 ;;
esac

archive="kado_${version}_${target_os}_${target_arch}.tar.gz"
for required in release-metadata.json release-metadata.json.sig release-public-key.pem checksums.txt "$archive"; do
  test -f "$release_directory/$required" || {
    printf 'missing release file: %%s\n' "$required" >&2
    exit 1
  }
done
expected="$(awk -v file="$archive" '$2 == file { print $1 }' "$release_directory/checksums.txt")"
test "${#expected}" -eq 64 || {
  printf 'archive checksum is missing\n' >&2
  exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$release_directory/$archive" | awk '{ print $1 }')"
else
  actual="$(shasum -a 256 "$release_directory/$archive" | awk '{ print $1 }')"
fi
test "$actual" = "$expected" || {
  printf 'archive checksum verification failed\n' >&2
  exit 1
}

temporary="$(mktemp -d "${TMPDIR:-/tmp}/kado-install.XXXXXX")"
cleanup() { rm -rf "$temporary"; }
trap cleanup EXIT HUP INT TERM
listing="$(tar -tzf "$release_directory/$archive")"
test "$listing" = "kado
LICENSE
INSTALL-CLI.md" || {
  printf 'archive contains unexpected paths\n' >&2
  exit 1
}
tar -xzf "$release_directory/$archive" -C "$temporary"
test -f "$temporary/kado" && test ! -L "$temporary/kado"
test "$(stat -f '%%Lp' "$temporary/kado" 2>/dev/null || stat -c '%%a' "$temporary/kado")" = "755"
provenance="$("$temporary/kado" version --json)"
printf '%%s\n' "$provenance" | grep -F "\"version\":\"${version}\"" >/dev/null
printf '%%s\n' "$provenance" | grep -F "\"target\":\"${target_os}/${target_arch}\"" >/dev/null
printf '%%s\n' "$provenance" | grep -F "\"release_key_id\":\"${expected_key_id}\"" >/dev/null
"$temporary/kado" release verify --directory "$release_directory" >/dev/null

parent="$(dirname "$destination")"
mkdir -p "$parent"
test ! -e "$destination" || {
  printf 'kado is already installed; use kado update\n' >&2
  exit 1
}
candidate="$(mktemp "$parent/.kado-candidate.XXXXXX")"
cp "$temporary/kado" "$candidate"
chmod 755 "$candidate"
if ! mv "$candidate" "$destination"; then
  exit 1
fi
printf 'installed kado %%s at %%s; credentials were unchanged\n' "$version" "$destination"
`, source.Version, keyID)
}

func uninstallUnixScript() string {
	return `#!/bin/sh
set -eu

destination="${KADO_INSTALL_PATH:-${HOME}/.local/bin/kado}"
confirm=no
purge=no
for argument in "$@"; do
  case "$argument" in
    --yes) confirm=yes ;;
    --purge-credentials) purge=yes ;;
    *) printf 'usage: uninstall.sh --yes [--purge-credentials]\n' >&2; exit 2 ;;
  esac
done
test "$confirm" = yes || {
  printf 'refusing to uninstall without --yes\n' >&2
  exit 2
}
test -f "$destination" && test ! -L "$destination" || {
  printf 'kado executable is not installed at %s\n' "$destination" >&2
  exit 1
}
if test "$purge" = yes; then
  "$destination" auth revoke
fi
rm -f "$destination"
if test "$purge" = yes; then
  printf 'removed kado executable after explicit credential revocation\n'
else
  printf 'removed kado executable; credentials were preserved\n'
fi
`
}

func installPowerShellScript(source releaseConfig, keyID string) string {
	return fmt.Sprintf(`param(
  [string]$ReleaseDirectory = ".",
  [string]$Destination = "$env:LOCALAPPDATA\Kado\kado.exe"
)
$ErrorActionPreference = "Stop"
$Version = %q
$ExpectedKeyId = %q
$Arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "unsupported architecture" }
}
$Archive = "kado_${Version}_windows_${Arch}.zip"
$Required = @("release-metadata.json", "release-metadata.json.sig", "release-public-key.pem", "checksums.txt", $Archive)
foreach ($Name in $Required) {
  if (-not (Test-Path -LiteralPath (Join-Path $ReleaseDirectory $Name) -PathType Leaf)) {
    throw "missing release file"
  }
}
$ChecksumLine = Get-Content -LiteralPath (Join-Path $ReleaseDirectory "checksums.txt") |
  Where-Object { $_ -match "^[0-9a-f]{64}  $([regex]::Escape($Archive))$" }
if (@($ChecksumLine).Count -ne 1) { throw "archive checksum is missing" }
$Expected = $ChecksumLine.Substring(0, 64)
$Actual = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $ReleaseDirectory $Archive)).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw "archive checksum verification failed" }
$ArchivePath = Join-Path $ReleaseDirectory $Archive
Add-Type -AssemblyName System.IO.Compression.FileSystem
$Zip = [System.IO.Compression.ZipFile]::OpenRead($ArchivePath)
try {
  $ZipEntries = @($Zip.Entries | ForEach-Object { $_.FullName } | Sort-Object)
  if (($ZipEntries -join ",") -ne "INSTALL-CLI.md,LICENSE,kado.exe") { throw "archive contains unexpected paths" }
} finally {
  $Zip.Dispose()
}
$Temporary = Join-Path ([System.IO.Path]::GetTempPath()) ("kado-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $Temporary | Out-Null
try {
  Expand-Archive -LiteralPath $ArchivePath -DestinationPath $Temporary
  $Entries = @(Get-ChildItem -LiteralPath $Temporary -File | ForEach-Object { $_.Name } | Sort-Object)
  if (($Entries -join ",") -ne "INSTALL-CLI.md,LICENSE,kado.exe") { throw "archive contains unexpected paths" }
  $CandidateBinary = Join-Path $Temporary "kado.exe"
  $Provenance = & $CandidateBinary version --json | ConvertFrom-Json
  if ($Provenance.version -ne $Version -or $Provenance.target -ne "windows/$Arch" -or $Provenance.release_key_id -ne $ExpectedKeyId) {
    throw "candidate executable provenance is invalid"
  }
  & $CandidateBinary release verify --directory $ReleaseDirectory | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "release bundle verification failed" }
  $Parent = Split-Path -Parent $Destination
  New-Item -ItemType Directory -Path $Parent -Force | Out-Null
  if (Test-Path -LiteralPath $Destination) { throw "kado is already installed; use kado update" }
  $Candidate = Join-Path $Parent (".kado-candidate-" + [guid]::NewGuid().ToString("N") + ".exe")
  Copy-Item -LiteralPath $CandidateBinary -Destination $Candidate
  Move-Item -LiteralPath $Candidate -Destination $Destination
  Write-Output "installed kado $Version at $Destination; credentials were unchanged"
} finally {
  Remove-Item -LiteralPath $Temporary -Recurse -Force -ErrorAction SilentlyContinue
}
`, source.Version, keyID)
}

func uninstallPowerShellScript() string {
	return `param(
  [switch]$Yes,
  [switch]$PurgeCredentials,
  [string]$Destination = "$env:LOCALAPPDATA\Kado\kado.exe"
)
$ErrorActionPreference = "Stop"
if (-not $Yes) { throw "refusing to uninstall without -Yes" }
if (-not (Test-Path -LiteralPath $Destination -PathType Leaf)) { throw "kado executable is not installed" }
if ($PurgeCredentials) {
  & $Destination auth revoke
  if ($LASTEXITCODE -ne 0) { throw "credential revocation failed; executable was retained" }
}
Remove-Item -LiteralPath $Destination -Force
if ($PurgeCredentials) {
  Write-Output "removed kado executable after explicit credential revocation"
} else {
  Write-Output "removed kado executable; credentials were preserved"
}
`
}
