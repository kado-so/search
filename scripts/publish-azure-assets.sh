#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: publish-azure-assets.sh upload|promote <release-directory>\n' >&2
  exit 2
}

[[ $# -eq 2 ]] || usage
operation="$1"
release_directory="$2"

: "${KADO_RELEASE_VERSION:?KADO_RELEASE_VERSION is required}"
: "${AZURE_STORAGE_ACCOUNT:?AZURE_STORAGE_ACCOUNT is required}"
: "${AZURE_STORAGE_CONTAINER:?AZURE_STORAGE_CONTAINER is required}"

[[ "$KADO_RELEASE_VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]] ||
  { printf 'KADO_RELEASE_VERSION is invalid\n' >&2; exit 2; }
[[ -d "$release_directory" ]] ||
  { printf 'release directory does not exist\n' >&2; exit 2; }

storage=(--account-name "$AZURE_STORAGE_ACCOUNT" \
  --container-name "$AZURE_STORAGE_CONTAINER" --auth-mode login)
storage_account=(--account-name "$AZURE_STORAGE_ACCOUNT" --auth-mode login)
immutable_cache='public,max-age=31536000,immutable'
channel_cache='no-cache,must-revalidate'
release_prefix="install/releases/$KADO_RELEASE_VERSION"

upload() {
  local source="$1"
  local destination="$2"
  local overwrite="$3"
  local cache_control="$4"
  local content_type="${5:-}"
  local arguments=(
    az storage blob upload
    "${storage[@]}"
    --file "$source"
    --name "$destination"
    --overwrite "$overwrite"
    --content-cache-control "$cache_control"
    --only-show-errors
  )
  if [[ -n "$content_type" ]]; then
    arguments+=(--content-type "$content_type")
  fi
  "${arguments[@]}"
}

case "$operation" in
  upload)
    existing="$(az storage blob list "${storage[@]}" \
      --prefix "$release_prefix/" --num-results 1 \
      --query 'length(@)' --output tsv --only-show-errors)"
    [[ "$existing" == "0" ]] || {
      printf 'immutable release prefix already exists: %s\n' "$release_prefix" >&2
      exit 1
    }

    az storage blob upload-batch "${storage_account[@]}" \
      --destination "$AZURE_STORAGE_CONTAINER" \
      --source "$release_directory" \
      --destination-path "$release_prefix" \
      --overwrite false \
      --content-cache-control "$immutable_cache" \
      --only-show-errors

    skill_version="$(sed -n 's/.*"version":"\([^"]*\)".*/\1/p' \
      "$release_directory/skill-metadata.json")"
    [[ "$skill_version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]] ||
      { printf 'skill version is invalid\n' >&2; exit 1; }
    skill_prefix="install/skills/kado-search/$skill_version"

    existing="$(az storage blob list "${storage[@]}" \
      --prefix "$skill_prefix/" --num-results 1 \
      --query 'length(@)' --output tsv --only-show-errors)"
    [[ "$existing" == "0" ]] || {
      printf 'immutable skill prefix already exists: %s\n' "$skill_prefix" >&2
      exit 1
    }
    upload "$release_directory/kado-search.tar.gz" \
      "$skill_prefix/kado-search.tar.gz" false "$immutable_cache" application/gzip
    upload "$release_directory/skill-metadata.json" \
      "$skill_prefix/metadata.json" false "$immutable_cache" application/json
    upload "$release_directory/skill-metadata.json.sig" \
      "$skill_prefix/metadata.json.sig" false "$immutable_cache" application/octet-stream

    verification_directory="$(mktemp -d)"
    trap 'rm -rf "$verification_directory"' EXIT
    az storage blob download-batch "${storage_account[@]}" \
      --source "$AZURE_STORAGE_CONTAINER" \
      --destination "$verification_directory" \
      --pattern "$release_prefix/*" \
      --overwrite \
      --only-show-errors
    while IFS= read -r -d '' source; do
      name="${source#"$release_directory"/}"
      destination="$verification_directory/$release_prefix/$name"
      cmp "$source" "$destination"
    done < <(find "$release_directory" -type f -print0 | sort -z)
    ;;

  promote)
    upload "$release_directory/install.sh" install.sh true "$channel_cache" text/x-shellscript
    upload "$release_directory/install.ps1" install.ps1 true "$channel_cache" text/plain
    upload "$release_directory/uninstall.sh" uninstall.sh true "$channel_cache" text/x-shellscript
    upload "$release_directory/uninstall.ps1" uninstall.ps1 true "$channel_cache" text/plain

    upload "$release_directory/release-metadata.json.sig" \
      install/releases/stable/release-metadata.json.sig \
      true "$channel_cache" application/octet-stream
    upload "$release_directory/release-metadata.json" \
      install/releases/stable/release-metadata.json \
      true "$channel_cache" application/json

    upload "$release_directory/kado-search.tar.gz" \
      install/skills/kado-search/latest/kado-search.tar.gz \
      true "$channel_cache" application/gzip
    upload "$release_directory/skill-metadata.json.sig" \
      install/skills/kado-search/latest/metadata.json.sig \
      true "$channel_cache" application/octet-stream
    upload "$release_directory/skill-metadata.json" \
      install/skills/kado-search/latest/metadata.json \
      true "$channel_cache" application/json
    ;;

  *)
    usage
    ;;
esac
