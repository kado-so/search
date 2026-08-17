package main

import "fmt"

func installGuide(source releaseIdentity, keyID string) string {
	return fmt.Sprintf(`# Install the Kado CLI

Version: %s
Source: %s
Install metadata: %s
Release signing key: %s

Download the complete release directory for your platform through a browser,
or install directly from the canonical Kado HTTPS boundary.

On Linux or macOS:

    curl -fsSL https://kado.so/install/install.sh | sh

or:

    wget -qO- https://kado.so/install/install.sh | sh

On Windows:

    powershell -NoProfile -Command "irm https://kado.so/install.ps1 | iex"

The installers require no superuser privileges. They install the latest
verified release or update an existing direct installation, configure the user
PATH when needed, install every compatible signed Kado skill in every
detected harness and the portable ~/.agents/skills location, and create or
reuse an authenticated Kado identity. Set
KADO_INSTALL_DIR to choose another user-owned executable directory or
KADO_NO_MODIFY_PATH=1 to leave shell configuration unchanged.

After installation:

    kado version --json
    kado auth status
    kado skill status

Use kado update for a signed update. Existing direct installations use one
explicit update to enter the launcher layout. Later verified releases activate
only for a new CLI start; a running command keeps its selected version.
Downgrades are rejected unless --allow-downgrade is explicit. Use the supplied
uninstall script with --yes; credentials are preserved unless
--purge-credentials is also explicit.
`, source.Version, source.Repository, source.InstallURL, keyID)
}

func installUnixScript(source releaseIdentity, keyID string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu

base_url=%q
install_dir="${KADO_INSTALL_DIR:-${HOME}/.local/bin}"
destination="$install_dir/kado"

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

download() {
  source_url="$1"
  output_path="$2"
  if command -v curl >/dev/null 2>&1; then
    curl --proto '=https' --tlsv1.2 -fsSL "$source_url" -o "$output_path"
  elif command -v wget >/dev/null 2>&1; then
    wget -q --https-only -O "$output_path" "$source_url"
  else
    printf 'kado installation requires curl or wget\n' >&2
    exit 1
  fi
}

executable_mode() {
  binary_path="$1"
  case "$target_os" in
    darwin) stat -f '%%Lp' "$binary_path" ;;
    linux) stat -c '%%a' "$binary_path" ;;
  esac
}

temporary="$(mktemp -d "${TMPDIR:-/tmp}/kado-install.XXXXXX")"
cleanup() { rm -rf "$temporary"; }
trap cleanup EXIT HUP INT TERM

if test -e "$destination" || test -L "$destination"; then
  test -f "$destination" && test ! -L "$destination" && test -x "$destination" || {
    printf 'existing kado destination is not a regular executable\n' >&2
    exit 1
  }
  if ! "$destination" update; then
    printf 'kado is installed, but the update failed; run kado update to retry\n' >&2
    exit 1
  fi
else
  metadata_url="$base_url/releases/stable/release-metadata.json"
  download "$metadata_url" "$temporary/release-metadata.json"
  download "$metadata_url.sig" "$temporary/release-metadata.json.sig"
  version="$(sed -n 's/.*"version":"\([^"]*\)".*/\1/p' "$temporary/release-metadata.json")"
  case "$version" in
    ''|*[!0-9A-Za-z.+-]*) printf 'release metadata version is invalid\n' >&2; exit 1 ;;
  esac
  archive="kado_${version}_${target_os}_${target_arch}.tar.gz"
  download "$base_url/releases/$version/$archive" "$temporary/$archive"
  listing="$(tar -tzf "$temporary/$archive")"
  test "$listing" = "kado
LICENSE
INSTALL-CLI.md" || {
    printf 'archive contains unexpected paths\n' >&2
    exit 1
  }
  tar -xzf "$temporary/$archive" -C "$temporary"
  test -f "$temporary/kado" && test ! -L "$temporary/kado"
  test "$(executable_mode "$temporary/kado")" = "755"
  identity="$("$temporary/kado" version --json)"
  printf '%%s\n' "$identity" | grep -F "\"version\":\"${version}\"" >/dev/null
  printf '%%s\n' "$identity" | grep -F "\"target\":\"${target_os}/${target_arch}\"" >/dev/null
  "$temporary/kado" release verify --directory "$temporary" >/dev/null

  mkdir -p "$install_dir"
  candidate="$(mktemp "$install_dir/.kado-candidate.XXXXXX")"
  cp "$temporary/kado" "$candidate"
  chmod 755 "$candidate"
  mv "$candidate" "$destination"
fi

receipt_candidate="$(mktemp "$install_dir/.kado-install.XXXXXX")"
printf '{"schema_version":1,"channel":"direct"}\n' >"$receipt_candidate"
chmod 600 "$receipt_candidate"
mv "$receipt_candidate" "$install_dir/kado.install.json"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    if test "${KADO_NO_MODIFY_PATH:-0}" != 1; then
      case "${SHELL:-}" in
        */zsh) profile="${ZDOTDIR:-$HOME}/.zshrc" ;;
        */bash) profile="$HOME/.bashrc" ;;
        */fish) profile="$HOME/.config/fish/config.fish" ;;
        *) profile="$HOME/.profile" ;;
      esac
      mkdir -p "$(dirname "$profile")"
      if ! grep -F '# >>> kado initialize >>>' "$profile" >/dev/null 2>&1; then
        {
          printf '\n# >>> kado initialize >>>\n'
          case "$profile" in
            */fish/config.fish) printf 'fish_add_path %%s\n' "$install_dir" ;;
            *) printf 'export PATH="%%s:$PATH"\n' "$install_dir" ;;
          esac
          printf '# <<< kado initialize <<<\n'
        } >>"$profile"
      fi
    fi
    ;;
esac

skills_ready=yes
if ! "$destination" skill install; then
  skills_ready=no
  printf 'kado is installed, but skill setup failed; run kado skill install to retry\n' >&2
fi
if ! "$destination" auth create; then
  printf 'kado is installed, but authentication setup failed; run kado auth create to retry\n' >&2
  exit 1
fi
if ! "$destination" auth status; then
  printf 'kado is installed and authentication was configured, but verification failed; run kado auth status to retry\n' >&2
  exit 1
fi
if test "$skills_ready" = yes; then
  printf 'kado is ready at %%s; skills synchronized and authentication verified\n' "$destination"
else
  printf 'kado is ready at %%s; authentication verified; skill setup requires retry\n' "$destination"
fi
`, source.InstallURL)
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
rm -f "$(dirname "$destination")/kado.install.json"
rm -rf "$destination.d"
if test "$purge" = yes; then
  printf 'removed kado executable after explicit credential revocation\n'
else
  printf 'removed kado executable; credentials were preserved\n'
fi
`
}

func installPowerShellScript(source releaseIdentity, keyID string) string {
	return fmt.Sprintf(`param(
  [string]$InstallDirectory = $(if ($env:KADO_INSTALL_DIR) { $env:KADO_INSTALL_DIR } else { "$env:LOCALAPPDATA\Kado" }),
  [switch]$NoModifyPath
)
$ErrorActionPreference = "Stop"
$BaseUrl = %q
$Destination = Join-Path $InstallDirectory "kado.exe"
$Arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "unsupported architecture" }
}
$Temporary = Join-Path ([System.IO.Path]::GetTempPath()) ("kado-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $Temporary | Out-Null
try {
  New-Item -ItemType Directory -Path $InstallDirectory -Force | Out-Null
  if (Test-Path -LiteralPath $Destination) {
    $Existing = Get-Item -LiteralPath $Destination -Force
    if ($Existing.PSIsContainer -or $Existing.LinkType) {
      throw "existing Kado destination is not a regular executable"
    }
    $KnownHelpers = @{}
    Get-ChildItem -LiteralPath $InstallDirectory -Filter ".kado-update-helper-*.exe" -ErrorAction SilentlyContinue |
      ForEach-Object { $KnownHelpers[$_.FullName] = $true }
    $UpdateOutput = @(& $Destination update)
    $UpdateExitCode = $LASTEXITCODE
    $UpdateOutput | Write-Output
    if ($UpdateExitCode -ne 0) {
      throw "Kado is installed, but the update failed; run 'kado update' to retry"
    }
    $UpdateFinished = $false
    for ($Attempt = 0; $Attempt -lt 300; $Attempt++) {
      $PendingHelpers = @(Get-ChildItem -LiteralPath $InstallDirectory -Filter ".kado-update-helper-*.exe" -ErrorAction SilentlyContinue |
        Where-Object { -not $KnownHelpers.ContainsKey($_.FullName) })
      $PendingHelpers | Remove-Item -Force -ErrorAction SilentlyContinue
      $PendingHelpers = @($PendingHelpers | Where-Object { Test-Path -LiteralPath $_.FullName })
      if ($PendingHelpers.Count -eq 0) {
        $UpdateFinished = $true
        break
      }
      Start-Sleep -Milliseconds 200
    }
    if (-not $UpdateFinished) {
      throw "Kado update did not finish; run 'kado update' to retry"
    }
  } else {
    $MetadataUrl = "$BaseUrl/releases/stable/release-metadata.json"
    Invoke-WebRequest -UseBasicParsing -Uri $MetadataUrl -OutFile (Join-Path $Temporary "release-metadata.json")
    Invoke-WebRequest -UseBasicParsing -Uri "$MetadataUrl.sig" -OutFile (Join-Path $Temporary "release-metadata.json.sig")
    $Metadata = Get-Content -Raw -LiteralPath (Join-Path $Temporary "release-metadata.json") | ConvertFrom-Json
    $Version = [string]$Metadata.version
    if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$') { throw "release metadata version is invalid" }
    $Archive = "kado_${Version}_windows_${Arch}.zip"
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/releases/$Version/$Archive" -OutFile (Join-Path $Temporary $Archive)
    Expand-Archive -LiteralPath (Join-Path $Temporary $Archive) -DestinationPath $Temporary
    $CandidateBinary = Join-Path $Temporary "kado.exe"
    $Identity = & $CandidateBinary version --json | ConvertFrom-Json
    if ($Identity.version -ne $Version -or $Identity.target -ne "windows/$Arch") {
      throw "candidate executable identity is invalid"
    }
    & $CandidateBinary release verify --directory $Temporary | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "release bundle verification failed" }

    $InstallCandidate = Join-Path $InstallDirectory (".kado-candidate-" + [guid]::NewGuid().ToString("N") + ".exe")
    Copy-Item -LiteralPath $CandidateBinary -Destination $InstallCandidate
    Move-Item -LiteralPath $InstallCandidate -Destination $Destination
  }

  $ReceiptCandidate = Join-Path $InstallDirectory (".kado-install-" + [guid]::NewGuid().ToString("N") + ".json")
  [System.IO.File]::WriteAllText(
    $ReceiptCandidate,
    '{"schema_version":1,"channel":"direct"}' + [Environment]::NewLine,
    [System.Text.UTF8Encoding]::new($false)
  )
  Move-Item -LiteralPath $ReceiptCandidate -Destination (Join-Path $InstallDirectory "kado.install.json") -Force

  $SkipPath = $NoModifyPath -or $env:KADO_NO_MODIFY_PATH -eq "1"
  if (-not $SkipPath) {
    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $Parts = @($UserPath -split ';' | Where-Object { $_ })
    if ($Parts -notcontains $InstallDirectory) {
      $NewPath = (@($Parts) + $InstallDirectory) -join ';'
      [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    }
  }
  & $Destination skill install
  $SkillsReady = $LASTEXITCODE -eq 0
  if (-not $SkillsReady) {
    Write-Warning "Kado is installed, but skill setup failed; run 'kado skill install' to retry"
  }
  & $Destination auth create
  if ($LASTEXITCODE -ne 0) {
    throw "Kado is installed, but authentication setup failed; run 'kado auth create' to retry"
  }
  & $Destination auth status
  if ($LASTEXITCODE -ne 0) {
    throw "Kado is installed and authentication was configured, but verification failed; run 'kado auth status' to retry"
  }
  if ($SkillsReady) {
    Write-Output "Kado is ready at $Destination; skills synchronized and authentication verified"
  } else {
    Write-Output "Kado is ready at $Destination; authentication verified; skill setup requires retry"
  }
} finally {
  Remove-Item -LiteralPath $Temporary -Recurse -Force -ErrorAction SilentlyContinue
}
`, source.InstallURL)
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
$InstallDirectory = Split-Path -Parent $Destination
Remove-Item -LiteralPath (Join-Path $InstallDirectory "kado.install.json") -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath ($Destination + ".d") -Recurse -Force -ErrorAction SilentlyContinue
if ($PurgeCredentials) {
  Write-Output "removed kado executable after explicit credential revocation"
} else {
  Write-Output "removed kado executable; credentials were preserved"
}
`
}
