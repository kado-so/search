# Auth API

Kado is hosted at `https://kado.so`. For local development, set `KADO_BASE_URL` to the local Search app URL. Credential caches are namespaced by that base URL, so local, staging, and production credentials cannot be confused.

Authentication precedence:

1. `KADO_API_KEY` supplied through the environment.
2. A cached browser-approved device token in user-local storage outside the current project.

Never use browser cookies. Never print, log, commit, or place credentials in the project. API keys expire after 180 days and can be revoked at `/account/api-keys`.

The examples require curl 8.3 or newer. They pass bearer credentials through curl's environment-variable expansion, not process arguments. Retry flags are used only for safe reads and idempotency-key-protected writes; the one-time device-token exchange is never blindly retried by curl.

## macOS and Linux

These examples require Bash, `curl`, `jq`, `cksum`, and restrictive user-local filesystem permissions.

Resolve an API key or cached device token without printing it, and prepare bounded curl policies:

```bash
KADO_BASE_URL="${KADO_BASE_URL:-https://kado.so}"
KADO_BASE_URL="${KADO_BASE_URL%/}"
KADO_ENVIRONMENT_ID="$(printf '%s' "$KADO_BASE_URL" | cksum | awk '{print $1}')"
KADO_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/kado/$KADO_ENVIRONMENT_ID"
KADO_TOKEN_FILE="$KADO_STATE_DIR/agent-auth.json"
KADO_PENDING_FILE="$KADO_STATE_DIR/pending-device-auth.json"
KADO_TOKEN="${KADO_API_KEY:-}"
KADO_CURL=(curl -sS --connect-timeout 10 --max-time 75)
KADO_RETRY=(--retry 2 --retry-all-errors --retry-delay 1)

umask 077
mkdir -p "$KADO_STATE_DIR"
chmod 700 "$KADO_STATE_DIR"
if [ -z "$KADO_TOKEN" ] && [ -f "$KADO_TOKEN_FILE" ]; then
  KADO_TOKEN="$(jq -er 'select((.expires_at // 0) > (now + 60)) | .access_token' "$KADO_TOKEN_FILE" 2>/dev/null || true)"
fi
```

If `KADO_TOKEN` is empty, resume a still-valid pending device authorization or create one. Pending device codes are private credentials and remain in the environment-specific user-state directory with mode `600`:

```bash
if [ -z "$KADO_TOKEN" ]; then
  PENDING_RESPONSE=""
  if [ -f "$KADO_PENDING_FILE" ]; then
    PENDING_RESPONSE="$(jq -ec 'select(.expires_at > (now + 5))' "$KADO_PENDING_FILE" 2>/dev/null || true)"
  fi

  if [ -z "$PENDING_RESPONSE" ]; then
    DEVICE_RESPONSE="$("${KADO_CURL[@]}" -X POST "$KADO_BASE_URL/api/auth/device/code" \
      -H 'content-type: application/json' -H 'accept: application/json' \
      --data-binary '{"client_id":"kado-cli"}')" || { printf 'Unable to start Kado device authorization.\n' >&2; return 1 2>/dev/null || exit 1; }
    PENDING_TMP="$KADO_PENDING_FILE.tmp.$$"
    printf '%s' "$DEVICE_RESPONSE" | jq '{device_code,user_code,verification_uri,verification_uri_complete,interval:(.interval // 5),expires_at:(now + (.expires_in | tonumber))}' > "$PENDING_TMP"
    chmod 600 "$PENDING_TMP"
    mv -f "$PENDING_TMP" "$KADO_PENDING_FILE"
    PENDING_RESPONSE="$(cat "$KADO_PENDING_FILE")"
  fi

  DEVICE_CODE="$(printf '%s' "$PENDING_RESPONSE" | jq -er '.device_code')"
  USER_CODE="$(printf '%s' "$PENDING_RESPONSE" | jq -r '.user_code // empty')"
  VERIFY_COMPLETE="$(printf '%s' "$PENDING_RESPONSE" | jq -r '.verification_uri_complete // empty')"
  VERIFY_BASE="$(printf '%s' "$PENDING_RESPONSE" | jq -er '.verification_uri')"
  INTERVAL="$(printf '%s' "$PENDING_RESPONSE" | jq -r '.interval // 5')"
  if [ -n "$VERIFY_COMPLETE" ]; then
    printf 'Open this URL to approve Kado access:\n%s\n' "$VERIFY_COMPLETE"
  else
    printf 'Open this URL to approve Kado access:\n%s\nEnter code: %s\n' "$VERIFY_BASE" "$USER_CODE"
  fi

  while [ -z "$KADO_TOKEN" ]; do
    sleep "$INTERVAL"
    if ! TOKEN_RESPONSE="$(printf '%s' "$DEVICE_CODE" | \
      jq -Rnc '{client_id:"kado-cli",device_code:input,grant_type:"urn:ietf:params:oauth:grant-type:device_code"}' | \
      "${KADO_CURL[@]}" -X POST "$KADO_BASE_URL/api/auth/device/token" \
        -H 'content-type: application/json' -H 'accept: application/json' --data-binary @-)"; then
      printf 'Transient Kado token transport failure; polling will continue.\n' >&2
      continue
    fi
    KADO_TOKEN="$(printf '%s' "$TOKEN_RESPONSE" | jq -r '.access_token // empty')"
    if [ -n "$KADO_TOKEN" ]; then
      TOKEN_TMP="$KADO_TOKEN_FILE.tmp.$$"
      printf '%s' "$TOKEN_RESPONSE" | jq '{access_token,token_type,expires_at:(now + (.expires_in | tonumber))}' > "$TOKEN_TMP"
      chmod 600 "$TOKEN_TMP"
      mv -f "$TOKEN_TMP" "$KADO_TOKEN_FILE"
      rm -f "$KADO_PENDING_FILE"
      break
    fi
    DEVICE_ERROR="$(printf '%s' "$TOKEN_RESPONSE" | jq -r '.error // "unknown_error"')"
    [ "$DEVICE_ERROR" = 'authorization_pending' ] && continue
    if [ "$DEVICE_ERROR" = 'slow_down' ]; then INTERVAL=$((INTERVAL + 5)); continue; fi
    rm -f "$KADO_PENDING_FILE"
    printf 'Kado device authorization failed: %s\n' "$DEVICE_ERROR" >&2
    unset DEVICE_CODE TOKEN_RESPONSE
    return 1 2>/dev/null || exit 1
  done
  unset DEVICE_CODE DEVICE_RESPONSE PENDING_RESPONSE TOKEN_RESPONSE
fi

export KADO_RUNTIME_TOKEN="$KADO_TOKEN"
KADO_AUTH=(--variable '%KADO_RUNTIME_TOKEN' --expand-header 'Authorization: Bearer {{KADO_RUNTIME_TOKEN}}')
```

Use `"${KADO_CURL[@]}" "${KADO_AUTH[@]}"` for an authenticated request. Add `"${KADO_RETRY[@]}"` only for safe reads or writes protected by an idempotency key. Remove `KADO_RUNTIME_TOKEN` when the Kado work is complete.

## Windows PowerShell

These examples require PowerShell and curl 8.3 or newer. Cached secrets and pending device codes are protected with Windows DPAPI for the current user.

```powershell
$KadoBaseUrl = if ($env:KADO_BASE_URL) { $env:KADO_BASE_URL.TrimEnd('/') } else { 'https://kado.so' }
$Hasher = [Security.Cryptography.SHA256]::Create()
try { $HashBytes = $Hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($KadoBaseUrl)) }
finally { $Hasher.Dispose() }
$KadoEnvironmentId = -join ($HashBytes[0..7] | ForEach-Object { $_.ToString('x2') })
$KadoStateDir = Join-Path (Join-Path $env:LOCALAPPDATA 'Kado') $KadoEnvironmentId
$KadoTokenFile = Join-Path $KadoStateDir 'agent-auth.clixml'
$KadoPendingFile = Join-Path $KadoStateDir 'pending-device-auth.clixml'
$KadoToken = $env:KADO_API_KEY
$KadoCurlArgs = @('-sS', '--connect-timeout', '10', '--max-time', '75')
$KadoRetryArgs = @('--retry', '2', '--retry-all-errors', '--retry-delay', '1')

New-Item -ItemType Directory -Force -Path $KadoStateDir | Out-Null
if ([string]::IsNullOrWhiteSpace($KadoToken) -and (Test-Path -LiteralPath $KadoTokenFile)) {
  $Cached = Import-Clixml -LiteralPath $KadoTokenFile
  if ([datetime]$Cached.ExpiresAt -gt (Get-Date).ToUniversalTime().AddMinutes(1)) {
    $Pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Cached.AccessToken)
    try { $KadoToken = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($Pointer) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($Pointer) }
  }
}
```

If `$KadoToken` is empty, resume a valid pending authorization or create one:

```powershell
if ([string]::IsNullOrWhiteSpace($KadoToken)) {
  $Pending = $null
  if (Test-Path -LiteralPath $KadoPendingFile) {
    $Candidate = Import-Clixml -LiteralPath $KadoPendingFile
    if ([datetime]$Candidate.ExpiresAt -gt (Get-Date).ToUniversalTime().AddSeconds(5)) { $Pending = $Candidate }
  }

  if ($null -eq $Pending) {
    $DeviceRequest = @{ client_id = 'kado-cli' } | ConvertTo-Json -Compress
    $DeviceText = $DeviceRequest | curl.exe @KadoCurlArgs -X POST "$KadoBaseUrl/api/auth/device/code" `
      -H 'content-type: application/json' -H 'accept: application/json' --data-binary '@-'
    if ($LASTEXITCODE -ne 0) { throw 'Unable to start Kado device authorization.' }
    $DeviceResponse = $DeviceText | ConvertFrom-Json
    $Pending = [pscustomobject]@{
      DeviceCode = ConvertTo-SecureString ([string]$DeviceResponse.device_code) -AsPlainText -Force
      UserCode = [string]$DeviceResponse.user_code
      VerificationUri = [string]$DeviceResponse.verification_uri
      VerificationUriComplete = [string]$DeviceResponse.verification_uri_complete
      Interval = if ($DeviceResponse.interval) { [int]$DeviceResponse.interval } else { 5 }
      ExpiresAt = (Get-Date).ToUniversalTime().AddSeconds([int]$DeviceResponse.expires_in)
    }
    $Pending | Export-Clixml -LiteralPath $KadoPendingFile -Force
  }

  $Pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Pending.DeviceCode)
  try { $DeviceCode = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($Pointer) }
  finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($Pointer) }
  $Interval = [int]$Pending.Interval
  if (-not [string]::IsNullOrWhiteSpace($Pending.VerificationUriComplete)) {
    Write-Host "Open this URL to approve Kado access:`n$($Pending.VerificationUriComplete)"
  } else {
    Write-Host "Open this URL to approve Kado access:`n$($Pending.VerificationUri)`nEnter code: $($Pending.UserCode)"
  }

  while ([string]::IsNullOrWhiteSpace($KadoToken)) {
    Start-Sleep -Seconds $Interval
    $TokenRequest = @{ client_id = 'kado-cli'; device_code = $DeviceCode; grant_type = 'urn:ietf:params:oauth:grant-type:device_code' } | ConvertTo-Json -Compress
    $TokenText = $TokenRequest | curl.exe @KadoCurlArgs -X POST "$KadoBaseUrl/api/auth/device/token" `
      -H 'content-type: application/json' -H 'accept: application/json' --data-binary '@-'
    if ($LASTEXITCODE -ne 0) { Write-Warning 'Transient Kado token transport failure; polling will continue.'; continue }
    $TokenResponse = $TokenText | ConvertFrom-Json
    if ($TokenResponse.access_token) {
      $KadoToken = [string]$TokenResponse.access_token
      [pscustomobject]@{
        AccessToken = ConvertTo-SecureString $KadoToken -AsPlainText -Force
        ExpiresAt = (Get-Date).ToUniversalTime().AddSeconds([int]$TokenResponse.expires_in)
      } | Export-Clixml -LiteralPath $KadoTokenFile -Force
      Remove-Item -LiteralPath $KadoPendingFile -Force -ErrorAction SilentlyContinue
      break
    }
    if ($TokenResponse.error -eq 'authorization_pending') { continue }
    if ($TokenResponse.error -eq 'slow_down') { $Interval += 5; continue }
    Remove-Item -LiteralPath $KadoPendingFile -Force -ErrorAction SilentlyContinue
    throw "Kado device authorization failed: $($TokenResponse.error)"
  }
  $DeviceCode = $null
  $TokenResponse = $null
}

$env:KADO_RUNTIME_TOKEN = $KadoToken
$KadoAuthArgs = @('--variable', '%KADO_RUNTIME_TOKEN', '--expand-header', 'Authorization: Bearer {{KADO_RUNTIME_TOKEN}}')
```

Use `curl.exe @KadoCurlArgs @KadoAuthArgs` for an authenticated request. Add `@KadoRetryArgs` only for safe reads or writes protected by an idempotency key. Remove `KADO_RUNTIME_TOKEN` when the Kado work is complete.

## Revoke or remove a device credential

Deleting the local cache does **not** revoke its Better Auth server session. Revoke the server session while the token is still available, then remove both environment-specific local state files.

macOS/Linux:

```bash
printf '%s' "$KADO_TOKEN" | jq -Rnc '{token:input}' | \
  "${KADO_CURL[@]}" "${KADO_AUTH[@]}" -X POST "$KADO_BASE_URL/api/auth/revoke-session" \
    -H 'content-type: application/json' -H 'accept: application/json' --data-binary @-
rm -f "$KADO_TOKEN_FILE" "$KADO_PENDING_FILE"
unset KADO_TOKEN KADO_RUNTIME_TOKEN
```

Windows PowerShell:

```powershell
$RevokeRequest = @{ token = $KadoToken } | ConvertTo-Json -Compress
$RevokeRequest | curl.exe @KadoCurlArgs @KadoAuthArgs -X POST "$KadoBaseUrl/api/auth/revoke-session" `
  -H 'content-type: application/json' -H 'accept: application/json' --data-binary '@-'
Remove-Item -LiteralPath $KadoTokenFile,$KadoPendingFile -Force -ErrorAction SilentlyContinue
Remove-Item Env:KADO_RUNTIME_TOKEN -ErrorAction SilentlyContinue
$KadoToken = $null
```
