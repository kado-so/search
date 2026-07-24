from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


REPOSITORY = Path(__file__).resolve().parents[1]
SKILL = REPOSITORY / "skills" / "kado-search"
SKILL_MD = SKILL / "SKILL.md"
EVALUATIONS = (
    REPOSITORY / "tests" / "fixtures" / "kado-search-evaluations.json"
)


def markdown_files() -> list[Path]:
    return sorted(SKILL.rglob("*.md"))


def combined_guidance() -> str:
    return "\n".join(path.read_text(encoding="utf-8") for path in markdown_files())


class KadoSearchSkillTests(unittest.TestCase):
    def test_frontmatter_schema_and_name(self) -> None:
        content = SKILL_MD.read_text(encoding="utf-8")
        match = re.match(r"^---\n(.*?)\n---\n", content, re.DOTALL)
        self.assertIsNotNone(match, "SKILL.md must start with YAML frontmatter")
        fields = parse_frontmatter(match.group(1))
        self.assertEqual(
            set(fields),
            {"name", "description", "license", "metadata"},
        )
        self.assertEqual(fields["name"], SKILL.name)
        self.assertRegex(fields["name"], r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
        self.assertGreater(len(fields["description"]), 80)
        self.assertLessEqual(len(fields["description"]), 1024)
        self.assertEqual(fields["license"], "MIT")
        self.assertEqual(
            fields["metadata"],
            {
                "author": "Kado",
                "version": "0.1.0",
                "homepage": "https://kado.so/install",
            },
        )
        self.assertIn("Use the installed `kado` CLI", content)
        self.assertIn("https://kado.so/install", content)

    def test_relative_references_exist_and_skill_has_one_owner(self) -> None:
        skill_files = [
            path
            for path in REPOSITORY.rglob("SKILL.md")
            if ".git" not in path.parts
        ]
        self.assertEqual(skill_files, [SKILL_MD])
        link_pattern = re.compile(r"\[[^\]]+\]\(([^)]+)\)")
        for markdown in markdown_files():
            for target in link_pattern.findall(markdown.read_text(encoding="utf-8")):
                if "://" in target or target.startswith("#"):
                    continue
                resolved = (markdown.parent / target.split("#", 1)[0]).resolve()
                self.assertTrue(
                    resolved.is_file(),
                    f"{markdown.relative_to(REPOSITORY)} has broken link {target}",
                )

    def test_obsolete_auth_and_http_implementations_are_absent(self) -> None:
        self.assertFalse((SKILL / "references" / "auth-api.md").exists())
        self.assertFalse((SKILL / "references" / "search-api.md").exists())
        guidance = combined_guidance()
        for forbidden in (
            "KADO_API_KEY",
            "KADO_AUTH_HEADER",
            "Authorization: Bearer",
            "/api/agent/",
            "access_token",
            "device_code",
            "verification_uri",
            "fetch(",
        ):
            self.assertNotIn(forbidden, guidance)

        bash_blocks = re.findall(
            r"^[ \t]*```bash[ \t]*\n(.*?)^[ \t]*```[ \t]*$",
            guidance,
            flags=re.DOTALL | re.MULTILINE,
        )
        self.assertGreater(len(bash_blocks), 0)
        for block in bash_blocks:
            for line in block.splitlines():
                command = line.strip()
                if command and not command.startswith("#"):
                    self.assertRegex(
                        command,
                        r"^kado (?:search|auth status|update --dry-run)\b",
                    )

    def test_openai_metadata_matches_skill(self) -> None:
        metadata = (SKILL / "agents" / "openai.yaml").read_text(encoding="utf-8")
        display = re.search(r'^  display_name: "([^"]+)"$', metadata, re.MULTILINE)
        short = re.search(
            r'^  short_description: "([^"]+)"$',
            metadata,
            re.MULTILINE,
        )
        prompt = re.search(r'^  default_prompt: "([^"]+)"$', metadata, re.MULTILINE)
        self.assertEqual(display.group(1), "Kado Search")
        self.assertGreaterEqual(len(short.group(1)), 25)
        self.assertLessEqual(len(short.group(1)), 64)
        self.assertIn("$kado-search", prompt.group(1))
        self.assertIn("allow_implicit_invocation: true", metadata)
        for icon in re.findall(r'^  icon_(?:small|large): "([^"]+)"$', metadata, re.MULTILINE):
            self.assertTrue((SKILL / icon).is_file(), f"missing metadata icon {icon}")

    def test_existing_manifests_reference_the_canonical_skill(self) -> None:
        codex_plugin = json.loads(
            (REPOSITORY / ".codex-plugin" / "plugin.json").read_text(
                encoding="utf-8"
            )
        )
        self.assertEqual(codex_plugin["name"], "kado-search")
        self.assertEqual(
            (REPOSITORY / codex_plugin["skills"]).resolve(),
            (REPOSITORY / "skills").resolve(),
        )
        codex_marketplace = json.loads(
            (REPOSITORY / ".agents" / "plugins" / "marketplace.json").read_text(
                encoding="utf-8"
            )
        )
        self.assertEqual(codex_marketplace["plugins"][0]["name"], "kado-search")
        self.assertEqual(
            codex_marketplace["plugins"][0]["source"],
            {"source": "local", "path": "./"},
        )
        claude_plugin = json.loads(
            (REPOSITORY / ".claude-plugin" / "plugin.json").read_text(
                encoding="utf-8"
            )
        )
        self.assertEqual(claude_plugin["name"], "kado-search")
        self.assertEqual(claude_plugin["version"], "0.1.0")

    def test_representative_trigger_and_lifecycle_evaluations(self) -> None:
        cases = json.loads(EVALUATIONS.read_text(encoding="utf-8"))
        names = {case["name"] for case in cases}
        self.assertEqual(len(names), len(cases))
        actions = {case["expected_action"] for case in cases}
        self.assertTrue(
            {
                "use_kado",
                "do_not_use_kado",
                "ask_then_use_kado",
                "answer_with_cli",
                "retry_once_with_cli",
                "safe_auth_status_only",
                "interrupt_cli_once",
            }.issubset(actions)
        )
        modes = {case["expected_mode"] for case in cases}
        self.assertTrue({"human", "json", "jsonl", "none"}.issubset(modes))

        guidance = combined_guidance().casefold()
        for case in cases:
            self.assertTrue(case["prompt"].strip(), case["name"])
            for evidence in case["evidence"]:
                self.assertIn(
                    evidence.casefold(),
                    guidance,
                    f"{case['name']} lacks guidance evidence {evidence!r}",
                )

    def test_cli_invocation_and_skill_flags_smoke(self) -> None:
        go = shutil.which("go")
        self.assertIsNotNone(go, "Go is required for the CLI smoke test")
        with tempfile.TemporaryDirectory(prefix="kado-skill-cli-") as temporary:
            executable = Path(temporary) / (
                "kado.exe" if os.name == "nt" else "kado"
            )
            subprocess.run(
                [go, "build", "-o", str(executable), "./cmd/kado"],
                cwd=REPOSITORY,
                check=True,
                capture_output=True,
                text=True,
            )
            help_result = subprocess.run(
                [executable, "help"],
                cwd=REPOSITORY,
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(help_result.returncode, 0)
            self.assertIn("search <query>", help_result.stdout)
            self.assertEqual(help_result.stderr, "")

            recognized_options = (
                ("--json",),
                ("--jsonl",),
                ("--width", "72"),
                ("--answer", "Web"),
                ("--timeout", "45s"),
                ("--first-page",),
                ("--retry",),
            )
            for option in recognized_options:
                result = subprocess.run(
                    [executable, "search", *option],
                    cwd=REPOSITORY,
                    check=False,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode, 2, option)
                self.assertIn("usage: kado search", result.stderr, option)
                self.assertNotIn("unknown search option", result.stderr, option)


def parse_frontmatter(content: str) -> dict[str, object]:
    fields: dict[str, object] = {}
    nested: dict[str, str] | None = None
    for line in content.splitlines():
        if line.startswith("  "):
            if nested is None:
                raise AssertionError(f"unexpected nested frontmatter line: {line}")
            key, separator, value = line.strip().partition(":")
            if separator != ":":
                raise AssertionError(f"invalid frontmatter line: {line}")
            nested[key] = json.loads(value.strip())
            continue
        key, separator, value = line.partition(":")
        if separator != ":":
            raise AssertionError(f"invalid frontmatter line: {line}")
        if value.strip():
            fields[key] = (
                json.loads(value.strip())
                if value.strip().startswith('"')
                else value.strip()
            )
            nested = None
        else:
            nested = {}
            fields[key] = nested
    return fields


if __name__ == "__main__":
    unittest.main()
