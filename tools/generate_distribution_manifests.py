#!/usr/bin/env python3
"""Generate Kado Search agent/plugin manifests from one distribution source."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from pathlib import Path
from typing import Any
from urllib.parse import urlparse


REPOSITORY = Path(__file__).resolve().parents[1]
SOURCE_PATH = REPOSITORY / "distribution" / "kado-search.manifest.json"
SCHEMA_PATH = REPOSITORY / "distribution" / "kado-search.manifest.schema.json"
INSTALLATION_SCHEMA_PATH = (
    REPOSITORY / "distribution" / "kado-installation.v1.schema.json"
)
INSTALLATION_DESCRIPTION_PATH = (
    REPOSITORY / "distribution" / "kado-installation.v1.gen.json"
)
INSTALLATION_MANIFEST_PATH = (
    REPOSITORY / "distribution" / "kado-installation.v1.manifest.gen.json"
)
VALIDATION_REQUIREMENTS_PATH = REPOSITORY / "tools" / "requirements-validation.txt"
SKILL_PATH = REPOSITORY / "skills" / "kado-search" / "SKILL.md"
EXPECTED_PLUGIN_ID = "kado-search"
EXPECTED_MARKETPLACE = "kado"
EXPECTED_SOURCE_KIND = "github"
EXPECTED_SOURCE_REPOSITORY = "kado-so/search"
EXPECTED_REPOSITORY_URL = (
    f"https://github.com/{EXPECTED_SOURCE_REPOSITORY}"
)
SEMVER = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$"
)
IDENTIFIER = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
MARKETPLACE = re.compile(r"^[A-Za-z0-9_-]+$")
HEX_COLOR = re.compile(r"^#[0-9A-Fa-f]{6}$")
COMMAND_TOKEN = re.compile(
    r"^(?:--[a-z][a-z-]*|[A-Za-z0-9][A-Za-z0-9@._/+~-]*)$"
)
FORBIDDEN_DISTRIBUTION_TEXT = (
    "curl ",
    "authorization: bearer",
    "access_token",
    "api key",
    "api_key",
    "client_secret",
    "private_key",
)


class ManifestError(ValueError):
    """A safe validation failure in canonical distribution metadata."""


def load_source(path: Path = SOURCE_PATH) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ManifestError(f"{display_path(path)} is not valid JSON") from error
    if not isinstance(value, dict):
        raise ManifestError("distribution source must be a JSON object")
    validate_source_schema(value)
    validate_source(value)
    return value


def validate_source_schema(source: dict[str, Any]) -> None:
    jsonschema = require_jsonschema()
    try:
        schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ManifestError(
            "distribution/kado-search.manifest.schema.json is not valid JSON"
        ) from error
    if not isinstance(schema, dict):
        raise ManifestError("distribution manifest schema must be a JSON object")
    if schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
        raise ManifestError("distribution manifest schema must declare Draft 2020-12")
    try:
        jsonschema.Draft202012Validator.check_schema(schema)
        validator = jsonschema.Draft202012Validator(
            schema,
            format_checker=jsonschema.FormatChecker(),
        )
        errors = sorted(
            validator.iter_errors(source),
            key=lambda error: [str(part) for part in error.absolute_path],
        )
    except jsonschema.exceptions.SchemaError as error:
        raise ManifestError("distribution manifest schema is invalid") from error
    if not errors:
        return
    first = errors[0]
    location = ".".join(str(part) for part in first.absolute_path) or "<root>"
    raise ManifestError(
        f"distribution source fails Draft 2020-12 validation at {location}: "
        f"{first.message}"
    )


def require_jsonschema() -> Any:
    try:
        import jsonschema
    except ImportError as error:
        raise ManifestError(
            "Draft 2020-12 validation requires the pinned validator environment; "
            "follow README 'Generate and validate distribution metadata' and "
            f"install {display_path(VALIDATION_REQUIREMENTS_PATH)}"
        ) from error
    return jsonschema


def validate_source(source: dict[str, Any]) -> None:
    require_keys(
        source,
        {"schema_version", "plugin", "skill", "marketplaces", "installation"},
        "distribution source",
    )
    if source["schema_version"] != 1:
        raise ManifestError("schema_version must be 1")

    plugin = require_object(source["plugin"], "plugin")
    require_keys(
        plugin,
        {
            "id",
            "version",
            "display_name",
            "description",
            "short_description",
            "long_description",
            "developer_name",
            "category",
            "capabilities",
            "homepage",
            "website",
            "repository",
            "license",
            "keywords",
            "brand_color",
            "default_prompt",
        },
        "plugin",
    )
    plugin_id = require_string(plugin["id"], "plugin.id")
    if (
        plugin_id != EXPECTED_PLUGIN_ID
        or len(plugin_id) > 64
        or IDENTIFIER.fullmatch(plugin_id) is None
    ):
        raise ManifestError(f"plugin.id must be {EXPECTED_PLUGIN_ID!r}")
    version = require_string(plugin["version"], "plugin.version")
    if len(version) > 48 or SEMVER.fullmatch(version) is None:
        raise ManifestError("plugin.version must be strict semver")
    for field in (
        "display_name",
        "description",
        "short_description",
        "long_description",
        "developer_name",
        "category",
        "license",
        "default_prompt",
    ):
        require_string(plugin[field], f"plugin.{field}")
    if len(plugin["short_description"]) > 64:
        raise ManifestError("plugin.short_description must not exceed 64 characters")
    if len(plugin["default_prompt"]) > 128:
        raise ManifestError("plugin.default_prompt must not exceed 128 characters")
    for field in ("homepage", "website", "repository"):
        require_https_url(plugin[field], f"plugin.{field}")
    if plugin["repository"] != EXPECTED_REPOSITORY_URL:
        raise ManifestError(
            f"plugin.repository must be {EXPECTED_REPOSITORY_URL!r}"
        )
    if HEX_COLOR.fullmatch(require_string(plugin["brand_color"], "plugin.brand_color")) is None:
        raise ManifestError("plugin.brand_color must use #RRGGBB")
    require_unique_strings(plugin["capabilities"], "plugin.capabilities")
    require_unique_strings(plugin["keywords"], "plugin.keywords")

    skill = require_object(source["skill"], "skill")
    require_keys(
        skill,
        {
            "path",
            "description",
            "compatibility",
            "implicit_invocation",
            "icon_small",
            "icon_large",
        },
        "skill",
    )
    if skill["path"] != f"skills/{plugin_id}":
        raise ManifestError("skill.path must match plugin.id")
    description = require_string(skill["description"], "skill.description")
    compatibility = require_string(skill["compatibility"], "skill.compatibility")
    if len(description) > 1024:
        raise ManifestError("skill.description must not exceed 1024 characters")
    if len(compatibility) > 500:
        raise ManifestError("skill.compatibility must not exceed 500 characters")
    if skill["implicit_invocation"] is not True:
        raise ManifestError("skill.implicit_invocation must be true")
    skill_root = resolve_repository_path(skill["path"], "skill.path")
    if skill_root.name != plugin_id:
        raise ManifestError("skill directory must match plugin.id")
    for field in ("icon_small", "icon_large"):
        relative = require_string(skill[field], f"skill.{field}")
        candidate = resolve_inside(skill_root, relative, f"skill.{field}")
        if not candidate.is_file():
            raise ManifestError(f"skill.{field} points to a missing file")

    marketplaces = require_object(source["marketplaces"], "marketplaces")
    require_keys(marketplaces, {"codex", "claude"}, "marketplaces")
    codex = require_object(marketplaces["codex"], "marketplaces.codex")
    require_keys(
        codex,
        {"name", "display_name", "source_path", "installation", "authentication"},
        "marketplaces.codex",
    )
    if (
        codex["name"] != EXPECTED_MARKETPLACE
        or MARKETPLACE.fullmatch(
            require_string(codex["name"], "marketplaces.codex.name")
        )
        is None
    ):
        raise ManifestError(
            f"marketplaces.codex.name must be {EXPECTED_MARKETPLACE!r}"
        )
    require_string(codex["display_name"], "marketplaces.codex.display_name")
    if codex["source_path"] != "./":
        raise ManifestError("marketplaces.codex.source_path must be ./")
    if codex["installation"] not in {
        "NOT_AVAILABLE",
        "AVAILABLE",
        "INSTALLED_BY_DEFAULT",
    }:
        raise ManifestError("marketplaces.codex.installation is invalid")
    if codex["authentication"] not in {"ON_INSTALL", "ON_USE"}:
        raise ManifestError("marketplaces.codex.authentication is invalid")

    claude = require_object(marketplaces["claude"], "marketplaces.claude")
    require_keys(claude, {"name", "description", "source_path"}, "marketplaces.claude")
    if (
        claude["name"] != EXPECTED_MARKETPLACE
        or IDENTIFIER.fullmatch(
            require_string(claude["name"], "marketplaces.claude.name")
        )
        is None
    ):
        raise ManifestError(
            f"marketplaces.claude.name must be {EXPECTED_MARKETPLACE!r}"
        )
    require_string(claude["description"], "marketplaces.claude.description")
    if claude["source_path"] != "./":
        raise ManifestError("marketplaces.claude.source_path must be ./")
    if codex["name"] != claude["name"]:
        raise ManifestError("Codex and Claude marketplace names must match")

    installation = require_object(source["installation"], "installation")
    require_keys(
        installation,
        {"cli_executable", "cli_install_url", "source"},
        "installation",
    )
    if installation["cli_executable"] != "kado":
        raise ManifestError("installation.cli_executable must be kado")
    install_url = require_https_url(
        installation["cli_install_url"], "installation.cli_install_url"
    )
    if install_url != plugin["homepage"]:
        raise ManifestError("plugin.homepage must equal installation.cli_install_url")
    source_spec = require_object(installation["source"], "installation.source")
    require_keys(source_spec, {"kind", "repository"}, "installation.source")
    if source_spec["kind"] != EXPECTED_SOURCE_KIND:
        raise ManifestError(
            f"installation.source.kind must be {EXPECTED_SOURCE_KIND!r}"
        )
    if source_spec["repository"] != EXPECTED_SOURCE_REPOSITORY:
        raise ManifestError(
            "installation.source.repository must be "
            f"{EXPECTED_SOURCE_REPOSITORY!r}"
        )
    if plugin["repository"] != f"https://github.com/{source_spec['repository']}":
        raise ManifestError(
            "plugin.repository must identify installation.source.repository"
        )
    installation_command_tokens(source)
    flattened = json.dumps(source, sort_keys=True).casefold()
    for forbidden in FORBIDDEN_DISTRIBUTION_TEXT:
        if forbidden in flattened:
            raise ManifestError(f"distribution source contains forbidden text {forbidden!r}")


def generated_files(source: dict[str, Any]) -> dict[Path, str]:
    plugin = source["plugin"]
    skill = source["skill"]
    marketplaces = source["marketplaces"]
    installation = source["installation"]
    author = {"name": plugin["developer_name"], "url": plugin["website"]}

    codex_plugin = {
        "name": plugin["id"],
        "version": plugin["version"],
        "description": plugin["description"],
        "author": author,
        "homepage": plugin["homepage"],
        "repository": plugin["repository"],
        "license": plugin["license"],
        "keywords": plugin["keywords"],
        "skills": "./skills/",
        "interface": {
            "displayName": plugin["display_name"],
            "shortDescription": plugin["short_description"],
            "longDescription": plugin["long_description"],
            "developerName": plugin["developer_name"],
            "category": plugin["category"],
            "capabilities": plugin["capabilities"],
            "websiteURL": plugin["website"],
            "defaultPrompt": [plugin["default_prompt"]],
            "composerIcon": f"./{skill['path']}/{skill['icon_small']}",
            "logo": f"./{skill['path']}/{skill['icon_large']}",
            "screenshots": [],
            "brandColor": plugin["brand_color"],
        },
    }
    codex_marketplace = {
        "name": marketplaces["codex"]["name"],
        "interface": {"displayName": marketplaces["codex"]["display_name"]},
        "plugins": [
            {
                "name": plugin["id"],
                "source": {
                    "source": "local",
                    "path": marketplaces["codex"]["source_path"],
                },
                "policy": {
                    "installation": marketplaces["codex"]["installation"],
                    "authentication": marketplaces["codex"]["authentication"],
                },
                "category": plugin["category"],
            }
        ],
    }
    claude_plugin = {
        "name": plugin["id"],
        "version": plugin["version"],
        "description": plugin["description"],
        "author": author,
        "homepage": plugin["homepage"],
        "repository": plugin["repository"],
        "license": plugin["license"],
        "keywords": plugin["keywords"],
        "skills": "./skills/",
    }
    claude_marketplace = {
        "name": marketplaces["claude"]["name"],
        "metadata": {"description": marketplaces["claude"]["description"]},
        "owner": author,
        "plugins": [
            {
                "name": plugin["id"],
                "description": plugin["description"],
                "author": author,
                "category": plugin["category"].casefold(),
                "source": marketplaces["claude"]["source_path"],
                "homepage": plugin["homepage"],
                "repository": plugin["repository"],
                "license": plugin["license"],
                "keywords": plugin["keywords"],
            }
        ],
    }
    openai_yaml = f"""interface:
  display_name: {yaml_string(plugin["display_name"])}
  short_description: {yaml_string(plugin["short_description"])}
  icon_small: {yaml_string(f"./{skill['icon_small']}")}
  icon_large: {yaml_string(f"./{skill['icon_large']}")}
  brand_color: {yaml_string(plugin["brand_color"])}
  default_prompt: {yaml_string(plugin["default_prompt"])}

policy:
  allow_implicit_invocation: {str(skill["implicit_invocation"]).lower()}
"""
    skill_frontmatter = f"""---
name: {plugin["id"]}
description: {yaml_string(skill["description"])}
license: {yaml_string(plugin["license"])}
metadata:
  author: {yaml_string(plugin["developer_name"])}
  version: {yaml_string(plugin["version"])}
  homepage: {yaml_string(plugin["homepage"])}
---
"""
    skill_body = current_skill_body()
    installation_description = build_installation_description(source)
    installation_description_document = json_document(installation_description)
    installation_manifest = build_installation_manifest(
        installation_description_document
    )
    install_document = render_install_document(source, installation_description)
    return {
        REPOSITORY / ".codex-plugin" / "plugin.json": json_document(codex_plugin),
        REPOSITORY / ".agents" / "plugins" / "marketplace.json": json_document(
            codex_marketplace
        ),
        REPOSITORY / ".claude-plugin" / "plugin.json": json_document(claude_plugin),
        REPOSITORY / ".claude-plugin" / "marketplace.json": json_document(
            claude_marketplace
        ),
        REPOSITORY / skill["path"] / "agents" / "openai.yaml": openai_yaml,
        SKILL_PATH: f"{skill_frontmatter}{skill_body}",
        REPOSITORY / "distribution" / "INSTALL.md": install_document,
        INSTALLATION_DESCRIPTION_PATH: installation_description_document,
        INSTALLATION_MANIFEST_PATH: json_document(installation_manifest),
    }


def build_installation_description(source: dict[str, Any]) -> dict[str, Any]:
    description = _installation_description_value(source)
    validate_installation_description(description, source)
    return description


def _installation_description_value(source: dict[str, Any]) -> dict[str, Any]:
    plugin = source["plugin"]
    skill = source["skill"]
    commands = installation_command_tokens(source)
    raw_base = "https://raw.githubusercontent.com/kado-so/search/main"
    repository_base = "https://github.com/kado-so/search"

    def command_steps(
        operations: dict[str, tuple[str, ...]],
    ) -> list[dict[str, object]]:
        return [
            {"operation": operation, "command": list(tokens)}
            for operation, tokens in operations.items()
        ]

    description: dict[str, Any] = {
        "$schema": (
            f"{raw_base}/distribution/kado-installation.v1.schema.json"
        ),
        "schema_version": "kado.installation.v1",
        "product": {
            "id": plugin["id"],
            "name": plugin["display_name"],
            "version": plugin["version"],
            "executable": source["installation"]["cli_executable"],
            "website_url": plugin["website"],
            "install_url": source["installation"]["cli_install_url"],
            "repository_url": plugin["repository"],
            "description_url": (
                f"{raw_base}/distribution/kado-installation.v1.gen.json"
            ),
            "description_manifest_url": (
                f"{raw_base}/distribution/"
                "kado-installation.v1.manifest.gen.json"
            ),
        },
        "approval": {
            "required": True,
            "kind": "explicit-user-confirmation",
            "applies_to": [
                "cli-install",
                "plugin-or-skill-install",
                "update-or-uninstall",
            ],
            "agent_behavior": [
                "Treat the exact user-authored phrase install kado.so as explicit confirmation to install Kado for the current supported agent.",
                "Before execution, state the exact target, source, filesystem changes, and network access; ask only when the target is ambiguous or no supported operation can be chosen safely.",
                "Updates, uninstall, and credential revocation require their own explicit user instructions.",
            ],
        },
        "capabilities": [
            "Authenticated current-market solution search",
            "Autonomous-agent enrollment with local non-exportable credential storage",
            "Canonical Search Document JSON and JSONL output",
            "Interactive clarification and bounded lifecycle handling",
            "Opaque pagination with deterministic multi-page output",
            "Verified signed self-update and credential-preserving uninstall",
        ],
        "supported_agents": [
            {
                "id": "agent-skills",
                "display_name": "Agent Skills",
                "package_kind": "agent-skill",
                "package_id": plugin["id"],
                "package_url": f"{repository_base}/tree/main/{skill['path']}",
                "manifest_urls": [
                    f"{raw_base}/{skill['path']}/SKILL.md",
                ],
                "install_steps": command_steps(
                    {"install": commands["agent_skills"]["install"]}
                ),
                "uninstall_steps": command_steps(
                    {"uninstall": commands["agent_skills"]["uninstall"]}
                ),
                "skill_invocation": plugin["id"],
            },
            {
                "id": "codex",
                "display_name": "Codex",
                "package_kind": "plugin",
                "package_id": (
                    f"{plugin['id']}@{source['marketplaces']['codex']['name']}"
                ),
                "package_url": repository_base,
                "manifest_urls": [
                    f"{raw_base}/.agents/plugins/marketplace.json",
                    f"{raw_base}/.codex-plugin/plugin.json",
                    f"{raw_base}/{skill['path']}/SKILL.md",
                ],
                "install_steps": command_steps(
                    {
                        "marketplace_add": commands["codex"]["marketplace_add"],
                        "install": commands["codex"]["install"],
                    }
                ),
                "uninstall_steps": command_steps(
                    {
                        "uninstall": commands["codex"]["uninstall"],
                        "marketplace_remove": commands["codex"][
                            "marketplace_remove"
                        ],
                    }
                ),
                "skill_invocation": f"{plugin['id']}:{plugin['id']}",
            },
            {
                "id": "claude-code",
                "display_name": "Claude Code",
                "package_kind": "plugin",
                "package_id": (
                    f"{plugin['id']}@{source['marketplaces']['claude']['name']}"
                ),
                "package_url": repository_base,
                "manifest_urls": [
                    f"{raw_base}/.claude-plugin/marketplace.json",
                    f"{raw_base}/.claude-plugin/plugin.json",
                    f"{raw_base}/{skill['path']}/SKILL.md",
                ],
                "install_steps": command_steps(
                    {
                        "marketplace_add": commands["claude"]["marketplace_add"],
                        "install": commands["claude"]["install"],
                    }
                ),
                "uninstall_steps": command_steps(
                    {
                        "uninstall": commands["claude"]["uninstall"],
                        "marketplace_remove": commands["claude"][
                            "marketplace_remove"
                        ],
                    }
                ),
                "skill_invocation": f"/{plugin['id']}:{plugin['id']}",
            },
        ],
        "supported_platforms": [
            {
                "os": goos,
                "arch": arch,
                "archive_format": "zip" if goos == "windows" else "tar.gz",
                "executable": "kado.exe" if goos == "windows" else "kado",
            }
            for goos in ("darwin", "linux", "windows")
            for arch in ("amd64", "arm64")
        ],
        "release": {
            "availability": "unpublished",
            "installable": False,
            "metadata_schema_version": "kado.release.v1",
            "metadata_url": (
                "https://kado.so/install/releases/stable/"
                "release-metadata.json"
            ),
            "signature_url": (
                "https://kado.so/install/releases/stable/"
                "release-metadata.json.sig"
            ),
            "publication_requirement": (
                "Do not claim that the CLI is downloadable or installable until "
                "canonical metadata and its detached signature are published "
                "and verify under reviewed release trust."
            ),
            "asset_discovery": {
                "binary": "targets[].binary",
                "archive": "targets[].archive",
                "install_guide": "install_guide",
                "install_unix": "install_unix",
                "install_powershell": "install_powershell",
                "uninstall_unix": "uninstall_unix",
                "uninstall_powershell": "uninstall_powershell",
            },
            "verification_discovery": {
                "public_key": {
                    "locator": (
                        "reviewed-release-bundle.release-public-key.pem"
                    ),
                    "format": "PEM SubjectPublicKeyInfo",
                },
                "signature": {
                    "locator": "release.signature_url",
                    "format": "Ed25519 detached signature",
                },
                "checksums": {
                    "locator": "release-metadata.checksums",
                    "format": "SHA-256 checksums",
                },
                "provenance": {
                    "locator": "release-metadata.provenance",
                    "format": "SLSA v1 in-toto statement",
                },
                "sbom": {
                    "locator": "release-metadata.targets[].sbom",
                    "format": "SPDX 2.3 JSON",
                },
            },
        },
        "cli": {
            "install": {
                "mode": "verified-local-release-bundle",
                "available_when": "signed-release-metadata-is-published",
                "steps": [
                    "Fetch canonical metadata and detached signature from the exact release discovery URLs.",
                    "Verify trusted Ed25519 metadata before following any artifact URL.",
                    "Download the selected platform archive and its checksums, provenance, SBOM, and generated installer into one local directory.",
                    "Verify every digest, provenance subject, SBOM identity, archive path, and candidate executable before installation.",
                    "Run only the downloaded generated installer after explicit user confirmation.",
                ],
                "forbidden": [
                    "curl-pipe-shell",
                    "unverified-binary",
                    "overwrite-existing-binary",
                ],
            },
            "update": {
                "command": ["kado", "update"],
                "dry_run_command": ["kado", "update", "--dry-run"],
                "approval_required": True,
                "behavior": (
                    "Verify signed same-origin release metadata and the complete "
                    "candidate supply chain before atomic replacement; reject "
                    "downgrades unless separately authorized."
                ),
            },
            "uninstall": {
                "command": ["kado", "uninstall", "--yes"],
                "approval_required": True,
                "behavior": (
                    "Remove only the executable and preserve autonomous-agent "
                    "credentials unless explicit purge and successful revocation "
                    "are separately requested."
                ),
            },
        },
        "service": {
            "protected_search": {
                "url": "https://kado.so/search",
                "method": "GET",
                "query_parameter": "q",
                "authentication_required": True,
                "media_type": "application/vnd.kado.search.v1+json",
            },
            "search_document": {
                "schema_version": "kado.search-document.v1",
                "manifest_url": (
                    "https://kado.so/contracts/search-document/v1/manifest.json"
                ),
                "schema_url": (
                    "https://kado.so/schemas/search-document/v1.json"
                ),
                "context_url": (
                    "https://kado.so/contexts/search-document/v1.jsonld"
                ),
                "openapi_url": (
                    "https://kado.so/openapi/search-document/v1.json"
                ),
            },
            "authentication": {
                "authorization_server_metadata_url": (
                    "https://kado.so/.well-known/oauth-authorization-server"
                ),
                "protected_resource_metadata_url": (
                    "https://kado.so/.well-known/oauth-protected-resource"
                ),
                "agent_principal_metadata_url": (
                    "https://kado.so/.well-known/agent-principal"
                ),
                "jwks_url": "https://kado.so/.well-known/jwks.json",
            },
        },
        "source_integrity": {
            "algorithm": "sha256",
            "distribution_source": source_artifact(
                "distribution/kado-search.manifest.json"
            ),
            "schema": source_artifact(
                "distribution/kado-installation.v1.schema.json"
            ),
        },
    }
    return description


def validate_installation_description(
    description: dict[str, Any],
    source: dict[str, Any] | None = None,
) -> None:
    jsonschema = require_jsonschema()
    try:
        schema = json.loads(
            INSTALLATION_SCHEMA_PATH.read_text(encoding="utf-8")
        )
        jsonschema.Draft202012Validator.check_schema(schema)
        validator = jsonschema.Draft202012Validator(
            schema,
            format_checker=jsonschema.FormatChecker(),
        )
        errors = sorted(
            validator.iter_errors(description),
            key=lambda error: [str(part) for part in error.absolute_path],
        )
    except (OSError, json.JSONDecodeError) as error:
        raise ManifestError(
            "distribution/kado-installation.v1.schema.json is invalid"
        ) from error
    except jsonschema.exceptions.SchemaError as error:
        raise ManifestError(
            "installation description schema is invalid"
        ) from error
    if errors:
        first = errors[0]
        location = ".".join(str(part) for part in first.absolute_path) or "<root>"
        raise ManifestError(
            "installation description fails Draft 2020-12 validation at "
            f"{location}: {first.message}"
        )
    canonical_source = source if source is not None else load_source()
    expected = _installation_description_value(canonical_source)
    difference = first_difference(expected, description)
    if difference is not None:
        raise ManifestError(
            "installation description semantic identity differs at "
            f"{difference}"
        )


def first_difference(expected: Any, actual: Any, path: str = "<root>") -> str | None:
    if type(expected) is not type(actual):
        return path
    if isinstance(expected, dict):
        if set(expected) != set(actual):
            return path
        for key in expected:
            difference = first_difference(
                expected[key],
                actual[key],
                f"{path}.{key}",
            )
            if difference is not None:
                return difference
        return None
    if isinstance(expected, list):
        if len(expected) != len(actual):
            return path
        for index, value in enumerate(expected):
            difference = first_difference(
                value,
                actual[index],
                f"{path}[{index}]",
            )
            if difference is not None:
                return difference
        return None
    return None if expected == actual else path


def build_installation_manifest(
    description_document: str,
) -> dict[str, Any]:
    return {
        "schema_version": "kado.installation-manifest.v1",
        "description_version": "kado.installation.v1",
        "artifacts": {
            "description": generated_artifact(
                "distribution/kado-installation.v1.gen.json",
                (
                    "https://raw.githubusercontent.com/kado-so/search/main/"
                    "distribution/kado-installation.v1.gen.json"
                ),
                description_document,
            ),
            "schema": generated_artifact(
                "distribution/kado-installation.v1.schema.json",
                (
                    "https://raw.githubusercontent.com/kado-so/search/main/"
                    "distribution/kado-installation.v1.schema.json"
                ),
                INSTALLATION_SCHEMA_PATH.read_text(encoding="utf-8"),
            ),
            "distribution_source": generated_artifact(
                "distribution/kado-search.manifest.json",
                (
                    "https://raw.githubusercontent.com/kado-so/search/main/"
                    "distribution/kado-search.manifest.json"
                ),
                SOURCE_PATH.read_text(encoding="utf-8"),
            ),
        },
    }


def generated_artifact(
    path: str,
    url: str,
    contents: str,
) -> dict[str, Any]:
    encoded = contents.encode("utf-8")
    return {
        "path": path,
        "url": url,
        "sha256": hashlib.sha256(encoded).hexdigest(),
        "size": len(encoded),
    }


def source_artifact(relative: str) -> dict[str, Any]:
    path = REPOSITORY / relative
    contents = path.read_bytes()
    return {
        "url": (
            "https://raw.githubusercontent.com/kado-so/search/main/"
            f"{relative}"
        ),
        "sha256": hashlib.sha256(contents).hexdigest(),
        "size": len(contents),
    }


def render_install_document(
    source: dict[str, Any],
    description: dict[str, Any],
) -> str:
    plugin = source["plugin"]
    installation = source["installation"]
    codex_marketplace = source["marketplaces"]["codex"]["name"]
    claude_marketplace = source["marketplaces"]["claude"]["name"]
    agents = {
        agent["id"]: agent
        for agent in description["supported_agents"]
    }

    def command(agent: str, operation: str) -> str:
        for group in ("install_steps", "uninstall_steps"):
            for step in agents[agent][group]:
                if step["operation"] == operation:
                    return render_command(tuple(step["command"]))
        raise ManifestError(
            f"installation description is missing {agent}.{operation}"
        )

    return f"""# Install Kado Search

This file is generated from
`distribution/kado-installation.v1.gen.json`, which in turn derives identity
and version from `distribution/kado-search.manifest.json`.
Do not edit it directly.

Every supported surface loads the one Agent Skills package at
`skills/{plugin["id"]}` and invokes the installed `{installation["cli_executable"]}`
executable. The CLI is required before the skill can perform Search; discover
its release availability at
[{installation["cli_install_url"]}]({installation["cli_install_url"]}).

CLI release availability is currently `{description["release"]["availability"]}`.
Do not claim that a downloadable CLI release exists until the canonical
metadata and detached signature resolve and verify. Once published, discover
checksums, provenance, per-platform SBOMs, archives, and generated local
installers through the signed release metadata at
[{description["release"]["metadata_url"]}]({description["release"]["metadata_url"]}).
Download the complete release bundle before running its generated installer;
no supported flow pipes a network response into a shell.

Installed release binaries support:

```bash
kado version --json
{render_command(tuple(description["cli"]["update"]["dry_run_command"]))}
{render_command(tuple(description["cli"]["update"]["command"]))}
{render_command(tuple(description["cli"]["uninstall"]["command"]))}
```

Uninstall preserves the autonomous-agent credential by default. Credential
revocation is separate and happens only when `--purge-credentials` is explicit.
The exact user-authored phrase `install kado.so` is explicit confirmation to
install Kado for the current supported agent. State the exact target, source,
filesystem changes, and network access before execution; ask only when the
target is ambiguous or no supported operation can be chosen safely. Updates,
uninstall, and credential revocation require their own explicit user
instructions.

## Agent Skills

Install:

```bash
{command("agent-skills", "install")}
```

Uninstall:

```bash
{command("agent-skills", "uninstall")}
```

The standalone skill invocation name is `{plugin["id"]}`.

## Codex Plugin

Install:

```bash
{command("codex", "marketplace_add")}
{command("codex", "install")}
```

Uninstall:

```bash
{command("codex", "uninstall")}
{command("codex", "marketplace_remove")}
```

The plugin ID is `{plugin["id"]}@{codex_marketplace}`. Codex presents its
skill under the plugin namespace `{plugin["id"]}:{plugin["id"]}`.

## Claude Code Plugin

Install:

```bash
{command("claude-code", "marketplace_add")}
{command("claude-code", "install")}
```

Uninstall:

```bash
{command("claude-code", "uninstall")}
{command("claude-code", "marketplace_remove")}
```

The plugin ID is `{plugin["id"]}@{claude_marketplace}` and the Claude skill
namespace is `/{plugin["id"]}:{plugin["id"]}`.

Plugin and skill removal does not remove the external Kado CLI or revoke its
installation identity. Use `kado auth revoke` only when the user explicitly
requests credential revocation.
"""


def write_or_check(files: dict[Path, str], check: bool) -> int:
    drift: list[str] = []
    for path, expected in files.items():
        if check:
            try:
                actual = path.read_text(encoding="utf-8")
            except OSError:
                drift.append(str(path.relative_to(REPOSITORY)))
                continue
            if actual != expected:
                drift.append(str(path.relative_to(REPOSITORY)))
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(expected, encoding="utf-8")
    if drift:
        print("Generated distribution manifests are stale:", file=sys.stderr)
        for path in drift:
            print(f"- {path}", file=sys.stderr)
        return 1
    return 0


def current_skill_body() -> str:
    try:
        content = SKILL_PATH.read_text(encoding="utf-8")
    except OSError as error:
        raise ManifestError("skills/kado-search/SKILL.md is missing") from error
    if not content.startswith("---\n"):
        raise ManifestError("skills/kado-search/SKILL.md frontmatter is missing")
    end = content.find("\n---\n", 4)
    if end < 0:
        raise ManifestError("skills/kado-search/SKILL.md frontmatter is not closed")
    return content[end + len("\n---\n") :]


def require_keys(value: dict[str, Any], keys: set[str], label: str) -> None:
    actual = set(value)
    if actual != keys:
        missing = sorted(keys - actual)
        extra = sorted(actual - keys)
        raise ManifestError(f"{label} keys differ; missing={missing}, extra={extra}")


def require_object(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise ManifestError(f"{label} must be an object")
    return value


def require_string(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ManifestError(f"{label} must be a non-empty string")
    return value


def require_https_url(value: Any, label: str) -> str:
    text = require_string(value, label)
    parsed = urlparse(text)
    if (
        parsed.scheme != "https"
        or not parsed.netloc
        or parsed.username is not None
        or parsed.password is not None
    ):
        raise ManifestError(f"{label} must be an absolute credential-free HTTPS URL")
    return text


def require_unique_strings(value: Any, label: str) -> list[str]:
    if (
        not isinstance(value, list)
        or not value
        or any(not isinstance(item, str) or not item.strip() for item in value)
        or len(value) != len(set(value))
    ):
        raise ManifestError(f"{label} must be a non-empty unique string array")
    return value


def installation_command_tokens(
    source: dict[str, Any],
) -> dict[str, dict[str, tuple[str, ...]]]:
    plugin = require_object(source.get("plugin"), "plugin")
    plugin_id = require_string(plugin.get("id"), "plugin.id")
    marketplaces = require_object(source.get("marketplaces"), "marketplaces")
    codex = require_object(marketplaces.get("codex"), "marketplaces.codex")
    claude = require_object(marketplaces.get("claude"), "marketplaces.claude")
    installation = require_object(source.get("installation"), "installation")
    source_spec = require_object(installation.get("source"), "installation.source")
    repository = require_string(
        source_spec.get("repository"),
        "installation.source.repository",
    )
    codex_marketplace = require_string(
        codex.get("name"),
        "marketplaces.codex.name",
    )
    claude_marketplace = require_string(
        claude.get("name"),
        "marketplaces.claude.name",
    )
    if (
        plugin_id != EXPECTED_PLUGIN_ID
        or repository != EXPECTED_SOURCE_REPOSITORY
        or codex_marketplace != EXPECTED_MARKETPLACE
        or claude_marketplace != EXPECTED_MARKETPLACE
    ):
        raise ManifestError(
            "installation commands require the exact canonical plugin, "
            "marketplace, and repository identities"
        )
    codex_plugin = f"{plugin_id}@{codex_marketplace}"
    claude_plugin = f"{plugin_id}@{claude_marketplace}"
    commands = {
        "agent_skills": {
            "install": (
                "npx",
                "skills",
                "add",
                repository,
                "--skill",
                plugin_id,
            ),
            "uninstall": ("npx", "skills", "remove", plugin_id),
        },
        "codex": {
            "marketplace_add": (
                "codex",
                "plugin",
                "marketplace",
                "add",
                repository,
            ),
            "install": ("codex", "plugin", "add", codex_plugin),
            "uninstall": ("codex", "plugin", "remove", codex_plugin),
            "marketplace_remove": (
                "codex",
                "plugin",
                "marketplace",
                "remove",
                codex_marketplace,
            ),
        },
        "claude": {
            "marketplace_add": (
                "claude",
                "plugin",
                "marketplace",
                "add",
                repository,
            ),
            "install": ("claude", "plugin", "install", claude_plugin),
            "uninstall": (
                "claude",
                "plugin",
                "uninstall",
                claude_plugin,
            ),
            "marketplace_remove": (
                "claude",
                "plugin",
                "marketplace",
                "remove",
                claude_marketplace,
            ),
        },
    }
    for operations in commands.values():
        for tokens in operations.values():
            render_command(tokens)
    return commands


def render_command(tokens: tuple[str, ...]) -> str:
    if (
        not isinstance(tokens, tuple)
        or not tokens
        or any(
            not isinstance(token, str)
            or COMMAND_TOKEN.fullmatch(token) is None
            for token in tokens
        )
    ):
        raise ManifestError(
            "generated installation commands require exact safe literal tokens"
        )
    return " ".join(tokens)


def resolve_repository_path(relative: Any, label: str) -> Path:
    text = require_string(relative, label)
    return resolve_inside(REPOSITORY, text, label)


def resolve_inside(root: Path, relative: str, label: str) -> Path:
    path = Path(relative)
    if path.is_absolute() or ".." in path.parts:
        raise ManifestError(f"{label} must stay inside its owner")
    candidate = (root / path).resolve()
    if not candidate.is_relative_to(root.resolve()):
        raise ManifestError(f"{label} must stay inside its owner")
    return candidate


def display_path(path: Path) -> str:
    try:
        return str(path.relative_to(REPOSITORY))
    except ValueError:
        return str(path)


def json_document(value: dict[str, Any]) -> str:
    return f"{json.dumps(value, indent=2, ensure_ascii=False)}\n"


def yaml_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate Kado Search distribution manifests."
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Fail if a generated consumer differs from the canonical source.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        source = load_source()
        return write_or_check(generated_files(source), args.check)
    except ManifestError as error:
        print(f"distribution manifest error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
