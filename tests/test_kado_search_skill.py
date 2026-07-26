from __future__ import annotations

import json
import re
import unittest
from pathlib import Path


REPOSITORY = Path(__file__).resolve().parents[1]
SKILL = REPOSITORY / "skills" / "kado-search"
SKILL_MD = SKILL / "SKILL.md"


class KadoSearchSkillTests(unittest.TestCase):
    def test_frontmatter_and_scope(self) -> None:
        content = SKILL_MD.read_text(encoding="utf-8")
        match = re.match(r"^---\n(.*?)\n---\n", content, re.DOTALL)
        self.assertIsNotNone(match, "SKILL.md must start with YAML frontmatter")
        fields = parse_frontmatter(match.group(1))
        self.assertEqual(
            set(fields),
            {"name", "description", "license", "metadata"},
        )
        self.assertEqual(fields["name"], "kado-search")
        self.assertEqual(fields["license"], "MIT")
        self.assertEqual(
            fields["metadata"],
            {
                "author": "Kado",
                "version": "0.1.0",
                "homepage": "https://kado.so",
            },
        )
        self.assertIn("This skill covers\nSearch only.", content)

        lowered = content.casefold()
        for forbidden in (
            "kado search --",
            "kado auth",
            "kado update",
            "kado uninstall",
            "install kado",
            "credential store",
            "release metadata",
            "sbom",
            "provenance",
            "api key",
            "browser cookie",
        ):
            self.assertNotIn(forbidden, lowered)

    def test_skill_has_no_instruction_references(self) -> None:
        markdown = list(SKILL.rglob("*.md"))
        self.assertEqual(markdown, [SKILL_MD])
        self.assertFalse((SKILL / "references").joinpath("cli-guide.md").exists())

    def test_openai_metadata_matches_skill(self) -> None:
        metadata = (SKILL / "agents" / "openai.yaml").read_text(encoding="utf-8")
        self.assertIn('display_name: "Kado Search"', metadata)
        self.assertIn("$kado-search", metadata)
        self.assertIn("allow_implicit_invocation: true", metadata)
        for icon in re.findall(
            r'^  icon_(?:small|large): "([^"]+)"$',
            metadata,
            re.MULTILINE,
        ):
            self.assertTrue((SKILL / icon).is_file(), f"missing icon {icon}")

    def test_direct_manifests_reference_the_skill(self) -> None:
        codex = json.loads(
            (REPOSITORY / ".codex-plugin" / "plugin.json").read_text(
                encoding="utf-8"
            )
        )
        claude = json.loads(
            (REPOSITORY / ".claude-plugin" / "plugin.json").read_text(
                encoding="utf-8"
            )
        )
        marketplace = json.loads(
            (REPOSITORY / ".agents" / "plugins" / "marketplace.json").read_text(
                encoding="utf-8"
            )
        )
        self.assertEqual(codex["name"], "kado-search")
        self.assertEqual(claude["name"], "kado-search")
        self.assertEqual(codex["skills"], "./skills/")
        self.assertEqual(claude["skills"], "./skills/")
        self.assertEqual(marketplace["plugins"][0]["name"], "kado-search")


def parse_frontmatter(content: str) -> dict[str, object]:
    fields: dict[str, object] = {}
    nested: dict[str, str] | None = None
    for line in content.splitlines():
        if line.startswith("  "):
            if nested is None:
                raise AssertionError(f"unexpected nested line: {line}")
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
