#!/usr/bin/env python3
"""Generate Kado Search agent/plugin manifests from one distribution source."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any
from urllib.parse import urlparse


REPOSITORY = Path(__file__).resolve().parents[1]
SOURCE_PATH = REPOSITORY / "distribution" / "kado-search.manifest.json"
SCHEMA_PATH = REPOSITORY / "distribution" / "kado-search.manifest.schema.json"
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
    if SEMVER.fullmatch(version) is None:
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
    install_document = render_install_document(source)
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
    }


def render_install_document(source: dict[str, Any]) -> str:
    plugin = source["plugin"]
    installation = source["installation"]
    codex_marketplace = source["marketplaces"]["codex"]["name"]
    claude_marketplace = source["marketplaces"]["claude"]["name"]
    commands = {
        surface: {
            operation: render_command(tokens)
            for operation, tokens in operations.items()
        }
        for surface, operations in installation_command_tokens(source).items()
    }
    return f"""# Install Kado Search

This file is generated from `distribution/kado-search.manifest.json`.
Do not edit it directly.

Every supported surface loads the one Agent Skills package at
`skills/{plugin["id"]}` and invokes the installed `{installation["cli_executable"]}`
executable. Install the CLI from [{installation["cli_install_url"]}]({installation["cli_install_url"]})
before using the skill. CLI binary release commands, checksums, updates, and
removal are published by the release phase and are intentionally not duplicated
here.

## Agent Skills

Install:

```bash
{commands["agent_skills"]["install"]}
```

Uninstall:

```bash
{commands["agent_skills"]["uninstall"]}
```

The standalone skill invocation name is `{plugin["id"]}`.

## Codex Plugin

Install:

```bash
{commands["codex"]["marketplace_add"]}
{commands["codex"]["install"]}
```

Uninstall:

```bash
{commands["codex"]["uninstall"]}
{commands["codex"]["marketplace_remove"]}
```

The plugin ID is `{plugin["id"]}@{codex_marketplace}`. Codex presents its
skill under the plugin namespace `{plugin["id"]}:{plugin["id"]}`.

## Claude Code Plugin

Install:

```bash
{commands["claude"]["marketplace_add"]}
{commands["claude"]["install"]}
```

Uninstall:

```bash
{commands["claude"]["uninstall"]}
{commands["claude"]["marketplace_remove"]}
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
