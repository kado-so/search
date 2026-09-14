#!/usr/bin/env bash
set -euo pipefail
umask 077

# Capture only the mutable public installer/CLI/skill channels. Run with the
# same Azure identity as publication, before writing any release objects.
[[ $# -eq 1 ]] || { printf 'usage: snapshot-azure-channels.sh <new-directory>\n' >&2; exit 2; }
: "${AZURE_STORAGE_ACCOUNT:?AZURE_STORAGE_ACCOUNT is required}"
: "${AZURE_STORAGE_CONTAINER:?AZURE_STORAGE_CONTAINER is required}"
snapshot_directory="$1"
[[ ! -e "$snapshot_directory" ]] || { printf 'snapshot directory already exists\n' >&2; exit 1; }
mkdir -p "$snapshot_directory/objects" "$snapshot_directory/properties"
storage=(--account-name "$AZURE_STORAGE_ACCOUNT" --container-name "$AZURE_STORAGE_CONTAINER" --auth-mode login)
channels=(
  install.sh install.ps1 uninstall.sh uninstall.ps1
  install/releases/stable/release-metadata.json
  install/releases/stable/release-metadata.json.sig
  install/skills/latest/catalog.json
  install/skills/latest/catalog.json.sig
)

for name in "${channels[@]}"; do
  properties="$snapshot_directory/properties/$name.json"
  object="$snapshot_directory/objects/$name"
  mkdir -p "$(dirname "$properties")" "$(dirname "$object")"
  az storage blob show "${storage[@]}" --name "$name" \
    --query '{name:name,etag:properties.etag,contentSettings:properties.contentSettings}' \
    --output json --only-show-errors >"$properties"
  etag="$(jq -er '.etag | select(type == "string" and length > 0)' "$properties")"
  # A concurrent modification must fail the snapshot, not capture mismatched
  # bytes and properties. No SAS URLs, access keys or signing keys are stored.
  az storage blob download "${storage[@]}" --name "$name" --file "$object" \
    --if-match "$etag" --overwrite false --only-show-errors --output none
done

# Recheck the complete set before marking it ready. This catches a channel
# promotion that overlapped our sequential reads, even for the earlier objects.
for name in "${channels[@]}"; do
  before="$(jq -er '.etag' "$snapshot_directory/properties/$name.json")"
  after="$(az storage blob show "${storage[@]}" --name "$name" --query properties.etag --output tsv --only-show-errors)"
  [[ "$before" = "$after" ]] || { printf 'channel changed during snapshot: %s\n' "$name" >&2; exit 1; }
done

(
  cd "$snapshot_directory"
  find objects properties -type f -print0 | sort -z | xargs -0 sha256sum >checksums.txt
  sha256sum --check checksums.txt
)
jq -n --arg account "$AZURE_STORAGE_ACCOUNT" --arg container "$AZURE_STORAGE_CONTAINER" \
  --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{schema_version:"kado.channel-snapshot.v1",account:$account,container:$container,created_at:$created,objects:8,status:"complete"}' \
  >"$snapshot_directory/snapshot.gen.json"
