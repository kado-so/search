#!/usr/bin/env bash
set -euo pipefail
export AZURE_STORAGE_ACCOUNT=fixture-account AZURE_STORAGE_CONTAINER=fixture-container
mkdir -p .kado-dev
root="$(mktemp -d .kado-dev/snapshot-test-XXXXXX)"
az() {
  local operation="$3" name='' file='' query='' expected=''
  shift 3
  while (($#)); do
    case "$1" in
      --name) name="$2"; shift 2 ;;
      --file) file="$2"; shift 2 ;;
      --query) query="$2"; shift 2 ;;
      --if-match) expected="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  if [[ "$operation" = show ]]; then
    if [[ "$query" = properties.etag ]]; then
      if [[ "${TEST_MODE:-}" = changed ]]; then printf '"v2"\n'; else printf '"v1"\n'; fi
    else
      jq -n --arg name "$name" '{name:$name,etag:"\"v1\"",contentSettings:{contentType:"application/octet-stream"},metadata:{}}'
    fi
  elif [[ "$operation" = download ]]; then
    [[ "$expected" = '"v1"' ]]
    [[ "${TEST_MODE:-}" != race ]] || return 33
    printf 'snapshot fixture: %s\n' "$name" >"$file"
  else
    printf 'unexpected mutation in read-only snapshot\n' >&2; return 99
  fi
}
export -f az
bash scripts/snapshot-azure-channels.sh "$root/success" >"$root/success.log"
test "$(jq -r .status "$root/success/snapshot.gen.json")" = complete
test "$(find "$root/success/objects" -type f | wc -l)" -eq 8
(cd "$root/success" && sha256sum --check checksums.txt >/dev/null)
if bash scripts/snapshot-azure-channels.sh "$root/success" >"$root/existing.log" 2>&1; then exit 1; fi
if TEST_MODE=race bash scripts/snapshot-azure-channels.sh "$root/race" >"$root/race.log" 2>&1; then exit 1; fi
test ! -e "$root/race/snapshot.gen.json"
if TEST_MODE=changed bash scripts/snapshot-azure-channels.sh "$root/changed" >"$root/changed.log" 2>&1; then exit 1; fi
test ! -e "$root/changed/snapshot.gen.json"
printf 'PASS: eight-object capture, checksums, existing-dir refusal, conditional download failure and concurrent-channel-change refusal; %s\n' "$root"
