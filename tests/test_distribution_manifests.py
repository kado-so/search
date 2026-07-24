from __future__ import annotations

import copy
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
from pathlib import Path


REPOSITORY = Path(__file__).resolve().parents[1]
SOURCE_PATH = REPOSITORY / "distribution" / "kado-search.manifest.json"
SCHEMA_PATH = REPOSITORY / "distribution" / "kado-search.manifest.schema.json"
GENERATE = REPOSITORY / "tools" / "generate_distribution_manifests.py"
SKILL = REPOSITORY / "skills" / "kado-search"
sys.path.insert(0, str(REPOSITORY / "tools"))
import generate_distribution_manifests as generator  # noqa: E402


def load_json(path: Path) -> dict[str, object]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise AssertionError(f"{path.relative_to(REPOSITORY)} must be an object")
    return value


class DistributionManifestTests(unittest.TestCase):
    def test_canonical_source_validates_against_published_schema(self) -> None:
        source = load_json(SOURCE_PATH)
        generator.validate_source_schema(source)
        self.assertEqual(
            load_json(SCHEMA_PATH)["$schema"],
            "https://json-schema.org/draft/2020-12/schema",
        )

    def test_schema_validation_dependency_is_mandatory_and_actionable(self) -> None:
        with mock.patch.dict(sys.modules, {"jsonschema": None}):
            with self.assertRaisesRegex(
                generator.ManifestError,
                r"pinned validator environment.*requirements-validation\.txt",
            ):
                generator.validate_source_schema(load_json(SOURCE_PATH))

    def test_generator_rejects_invalid_versions_paths_and_homepage(self) -> None:
        source = generator.load_source()
        invalid_cases = []

        bad_version = copy.deepcopy(source)
        bad_version["plugin"]["version"] = "latest"
        invalid_cases.append(bad_version)

        escaping_icon = copy.deepcopy(source)
        escaping_icon["skill"]["icon_small"] = "../../outside.svg"
        invalid_cases.append(escaping_icon)

        mismatched_homepage = copy.deepcopy(source)
        mismatched_homepage["plugin"]["homepage"] = "https://example.test/install"
        invalid_cases.append(mismatched_homepage)

        for invalid in invalid_cases:
            with self.subTest(invalid=invalid):
                with self.assertRaises(generator.ManifestError):
                    generator.validate_source(invalid)

    def test_schema_and_generator_reject_unsafe_install_identities(self) -> None:
        source = generator.load_source()
        invalid_cases: list[tuple[str, dict[str, object]]] = []
        unsafe_sources = {
            "command substitution": "kado-so/search$(id)",
            "backticks": "kado-so/search`id`",
            "redirect": "kado-so/search>output",
            "quoted token": 'kado-so/search"other"',
            "escaped token": r"kado-so/search\other",
            "space": "kado-so/search other",
            "tab": "kado-so/search\tother",
            "newline": "kado-so/search\nother",
            "wrong source": "other/search",
        }
        for label, value in unsafe_sources.items():
            invalid = copy.deepcopy(source)
            invalid["installation"]["source"]["repository"] = value
            invalid_cases.append((label, invalid))

        wrong_plugin = copy.deepcopy(source)
        wrong_plugin["plugin"]["id"] = "other-search"
        invalid_cases.append(("wrong plugin", wrong_plugin))

        wrong_marketplace = copy.deepcopy(source)
        wrong_marketplace["marketplaces"]["codex"]["name"] = "other"
        wrong_marketplace["marketplaces"]["claude"]["name"] = "other"
        invalid_cases.append(("wrong marketplace", wrong_marketplace))

        wrong_local_source = copy.deepcopy(source)
        wrong_local_source["marketplaces"]["codex"]["source_path"] = "./other"
        invalid_cases.append(("wrong local source", wrong_local_source))

        swapped_commands = copy.deepcopy(source)
        swapped_commands["installation"]["codex"] = {
            "install": "codex plugin remove kado-search@kado",
            "uninstall": "codex plugin add kado-search@kado",
        }
        invalid_cases.append(("swapped legacy commands", swapped_commands))

        injected_commands = copy.deepcopy(source)
        injected_commands["installation"]["agent_skills"] = {
            "install": "npx skills add kado-so/search$(id)",
            "uninstall": "npx skills remove kado-search > output",
        }
        invalid_cases.append(("injected legacy commands", injected_commands))

        for label, invalid in invalid_cases:
            with self.subTest(label=label, validator="schema"):
                with self.assertRaises(generator.ManifestError):
                    generator.validate_source_schema(invalid)
            with self.subTest(label=label, validator="generator"):
                with self.assertRaises(generator.ManifestError):
                    generator.validate_source(invalid)

    def test_install_commands_are_derived_as_exact_literal_tokens(self) -> None:
        source = generator.load_source()
        self.assertEqual(
            generator.installation_command_tokens(source),
            {
                "agent_skills": {
                    "install": (
                        "npx",
                        "skills",
                        "add",
                        "kado-so/search",
                        "--skill",
                        "kado-search",
                    ),
                    "uninstall": (
                        "npx",
                        "skills",
                        "remove",
                        "kado-search",
                    ),
                },
                "codex": {
                    "marketplace_add": (
                        "codex",
                        "plugin",
                        "marketplace",
                        "add",
                        "kado-so/search",
                    ),
                    "install": (
                        "codex",
                        "plugin",
                        "add",
                        "kado-search@kado",
                    ),
                    "uninstall": (
                        "codex",
                        "plugin",
                        "remove",
                        "kado-search@kado",
                    ),
                    "marketplace_remove": (
                        "codex",
                        "plugin",
                        "marketplace",
                        "remove",
                        "kado",
                    ),
                },
                "claude": {
                    "marketplace_add": (
                        "claude",
                        "plugin",
                        "marketplace",
                        "add",
                        "kado-so/search",
                    ),
                    "install": (
                        "claude",
                        "plugin",
                        "install",
                        "kado-search@kado",
                    ),
                    "uninstall": (
                        "claude",
                        "plugin",
                        "uninstall",
                        "kado-search@kado",
                    ),
                    "marketplace_remove": (
                        "claude",
                        "plugin",
                        "marketplace",
                        "remove",
                        "kado",
                    ),
                },
            },
        )
        installation = source["installation"]
        self.assertEqual(
            set(installation),
            {"cli_executable", "cli_install_url", "source"},
        )
        for operations in generator.installation_command_tokens(source).values():
            for tokens in operations.values():
                command = generator.render_command(tokens)
                self.assertEqual(command, " ".join(tokens))
                self.assertEqual(command.split(" "), list(tokens))
                for token in tokens:
                    self.assertRegex(token, generator.COMMAND_TOKEN)
                    self.assertNotRegex(token, r"[\s$`()<>|;&\\\"']")

    def test_generated_consumers_are_current(self) -> None:
        result = subprocess.run(
            [sys.executable, "-B", str(GENERATE), "--check"],
            cwd=REPOSITORY,
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_ids_versions_metadata_and_capabilities_do_not_drift(self) -> None:
        source = load_json(SOURCE_PATH)
        plugin = source["plugin"]
        skill = source["skill"]
        marketplaces = source["marketplaces"]
        codex_plugin = load_json(REPOSITORY / ".codex-plugin" / "plugin.json")
        codex_marketplace = load_json(
            REPOSITORY / ".agents" / "plugins" / "marketplace.json"
        )
        claude_plugin = load_json(REPOSITORY / ".claude-plugin" / "plugin.json")
        claude_marketplace = load_json(
            REPOSITORY / ".claude-plugin" / "marketplace.json"
        )

        self.assertEqual(codex_plugin["name"], plugin["id"])
        self.assertEqual(claude_plugin["name"], plugin["id"])
        self.assertEqual(codex_plugin["version"], plugin["version"])
        self.assertEqual(claude_plugin["version"], plugin["version"])
        self.assertEqual(codex_plugin["homepage"], plugin["homepage"])
        self.assertEqual(claude_plugin["homepage"], plugin["homepage"])
        self.assertEqual(
            codex_plugin["interface"]["capabilities"],
            plugin["capabilities"],
        )
        for manifest in (codex_plugin, claude_plugin):
            self.assertNotIn("hooks", manifest)
            self.assertNotIn("mcpServers", manifest)
            self.assertNotIn("apps", manifest)
            self.assertNotIn("permissions", manifest)
            self.assertEqual(manifest["skills"], "./skills/")
        self.assertEqual(
            codex_plugin["interface"]["composerIcon"],
            f"./{skill['path']}/{skill['icon_small']}",
        )
        self.assertEqual(
            codex_plugin["interface"]["logo"],
            f"./{skill['path']}/{skill['icon_large']}",
        )

        codex_entry = codex_marketplace["plugins"][0]
        claude_entry = claude_marketplace["plugins"][0]
        self.assertEqual(
            f"{codex_entry['name']}@{codex_marketplace['name']}",
            "kado-search@kado",
        )
        self.assertEqual(
            f"{claude_entry['name']}@{claude_marketplace['name']}",
            "kado-search@kado",
        )
        self.assertEqual(
            codex_entry["source"],
            {
                "source": "local",
                "path": marketplaces["codex"]["source_path"],
            },
        )
        self.assertEqual(
            claude_entry["source"],
            marketplaces["claude"]["source_path"],
        )
        self.assertEqual(
            codex_entry["policy"],
            {
                "installation": "AVAILABLE",
                "authentication": "ON_INSTALL",
            },
        )

    def test_all_relative_references_stay_in_the_single_plugin_package(self) -> None:
        source = load_json(SOURCE_PATH)
        skill_path = REPOSITORY / source["skill"]["path"]
        self.assertEqual(
            [
                path.relative_to(REPOSITORY)
                for path in REPOSITORY.rglob("SKILL.md")
                if ".git" not in path.parts
            ],
            [Path("skills/kado-search/SKILL.md")],
        )
        self.assertTrue(skill_path.is_dir())

        codex = load_json(REPOSITORY / ".codex-plugin" / "plugin.json")
        claude = load_json(REPOSITORY / ".claude-plugin" / "plugin.json")
        for manifest in (codex, claude):
            skills_root = resolve_inside(REPOSITORY, manifest["skills"])
            self.assertEqual(skills_root, (REPOSITORY / "skills").resolve())
            self.assertEqual(
                [path.name for path in skills_root.iterdir() if path.is_dir()],
                ["kado-search"],
            )

        interface = codex["interface"]
        for field in ("composerIcon", "logo"):
            self.assertTrue(resolve_inside(REPOSITORY, interface[field]).is_file())
        for link in markdown_links(SKILL):
            self.assertTrue(link.is_file(), f"broken skill reference: {link}")

    def test_install_reference_uses_only_declared_safe_commands(self) -> None:
        source = load_json(SOURCE_PATH)
        install = source["installation"]
        commands = generator.installation_command_tokens(source)
        document = (REPOSITORY / "distribution" / "INSTALL.md").read_text(
            encoding="utf-8"
        )
        for operations in commands.values():
            for tokens in operations.values():
                self.assertIn(generator.render_command(tokens), document)
        self.assertIn(install["cli_install_url"], document)
        self.assertIn("kado-search:kado-search", document)
        self.assertIn("/kado-search:kado-search", document)
        for forbidden in (
            "curl ",
            "Authorization: Bearer",
            "access_token",
            "client_secret",
            "private_key",
        ):
            self.assertNotIn(forbidden, document)

    def test_installed_manifest_validators(self) -> None:
        claude = shutil.which("claude")
        if claude is None:
            self.skipTest("Claude Code is not installed")
        for path in (
            REPOSITORY / ".claude-plugin" / "plugin.json",
            REPOSITORY / ".claude-plugin" / "marketplace.json",
        ):
            result = run([claude, "plugin", "validate", str(path)])
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


@unittest.skipUnless(
    os.environ.get("KADO_DISTRIBUTION_INSTALL_SMOKE") == "1",
    "set KADO_DISTRIBUTION_INSTALL_SMOKE=1 for clean install smoke tests",
)
class DistributionInstallSmokeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.temporary = tempfile.TemporaryDirectory(prefix="kado-distribution-smoke-")
        cls.root = Path(cls.temporary.name)
        cls.bin = cls.root / "bin"
        cls.bin.mkdir()
        go = shutil.which("go")
        if go is None:
            raise unittest.SkipTest("Go is required for install smoke tests")
        cls.kado = cls.bin / ("kado.exe" if os.name == "nt" else "kado")
        result = run(
            [go, "build", "-o", str(cls.kado), "./cmd/kado"],
            cwd=REPOSITORY,
        )
        if result.returncode != 0:
            raise AssertionError(result.stdout + result.stderr)

    @classmethod
    def tearDownClass(cls) -> None:
        cls.temporary.cleanup()

    def test_codex_clean_install_discovery_and_uninstall(self) -> None:
        codex = shutil.which("codex")
        if codex is None:
            self.skipTest("Codex CLI is not installed")
        home = self.root / "codex"
        home.mkdir()
        env = smoke_environment(home, self.bin)

        added = run(
            [codex, "plugin", "marketplace", "add", str(REPOSITORY), "--json"],
            env=env,
        )
        self.assertEqual(added.returncode, 0, added.stdout + added.stderr)
        available = json.loads(
            successful(
                run(
                    [codex, "plugin", "list", "--available", "--json"],
                    env=env,
                )
            )
        )
        self.assertEqual(
            [plugin["pluginId"] for plugin in available["available"]],
            ["kado-search@kado"],
        )
        installed = json.loads(
            successful(
                run(
                    [codex, "plugin", "add", "kado-search@kado", "--json"],
                    env=env,
                )
            )
        )
        self.assertEqual(installed["version"], "0.1.0")
        assert_installed_skill(Path(installed["installedPath"]))
        self.assertEqual(run(["kado", "help"], env=env).returncode, 0)
        successful(
            run(
                [codex, "plugin", "remove", "kado-search@kado", "--json"],
                env=env,
            )
        )
        after = json.loads(
            successful(run([codex, "plugin", "list", "--json"], env=env))
        )
        self.assertEqual(after["installed"], [])
        successful(
            run(
                [codex, "plugin", "marketplace", "remove", "kado"],
                env=env,
            )
        )

    def test_claude_clean_install_discovery_and_uninstall(self) -> None:
        claude = shutil.which("claude")
        if claude is None:
            self.skipTest("Claude Code is not installed")
        home = self.root / "claude"
        home.mkdir()
        env = smoke_environment(home, self.bin)
        successful(
            run(
                [claude, "plugin", "marketplace", "add", str(REPOSITORY)],
                env=env,
            )
        )
        available = json.loads(
            successful(
                run(
                    [claude, "plugin", "list", "--available", "--json"],
                    env=env,
                )
            )
        )
        self.assertEqual(
            [plugin["pluginId"] for plugin in available["available"]],
            ["kado-search@kado"],
        )
        successful(
            run(
                [
                    claude,
                    "plugin",
                    "install",
                    "kado-search@kado",
                    "--scope",
                    "user",
                ],
                env=env,
            )
        )
        installed = json.loads(
            successful(run([claude, "plugin", "list", "--json"], env=env))
        )
        self.assertEqual(len(installed), 1)
        self.assertEqual(installed[0]["id"], "kado-search@kado")
        self.assertEqual(installed[0]["version"], "0.1.0")
        assert_installed_skill(Path(installed[0]["installPath"]))
        self.assertEqual(run(["kado", "help"], env=env).returncode, 0)
        successful(
            run(
                [
                    claude,
                    "plugin",
                    "uninstall",
                    "kado-search@kado",
                    "--scope",
                    "user",
                ],
                env=env,
            )
        )
        self.assertEqual(
            json.loads(
                successful(run([claude, "plugin", "list", "--json"], env=env))
            ),
            [],
        )
        successful(
            run(
                [claude, "plugin", "marketplace", "remove", "kado"],
                env=env,
            )
        )

    def test_agent_skills_clean_install_discovery_and_uninstall(self) -> None:
        npx = shutil.which("npx")
        if npx is None:
            self.skipTest("npx is not installed")
        home = self.root / "agent-skills-home"
        project = self.root / "agent-skills-project"
        home.mkdir()
        project.mkdir()
        env = smoke_environment(home, self.bin)
        successful(
            run(
                [
                    npx,
                    "--yes",
                    "skills",
                    "add",
                    str(REPOSITORY),
                    "--skill",
                    "kado-search",
                    "--agent",
                    "codex",
                    "--copy",
                    "-y",
                ],
                cwd=project,
                env=env,
                timeout=120,
            )
        )
        installed = project / ".agents" / "skills" / "kado-search"
        assert_installed_skill(installed)
        listing = json.loads(
            successful(
                run(
                    [npx, "--yes", "skills", "list", "--json"],
                    cwd=project,
                    env=env,
                    timeout=120,
                )
            )
        )
        self.assertEqual([skill["name"] for skill in listing], ["kado-search"])
        self.assertEqual(run(["kado", "help"], env=env).returncode, 0)
        successful(
            run(
                [
                    npx,
                    "--yes",
                    "skills",
                    "remove",
                    "kado-search",
                    "--agent",
                    "codex",
                    "-y",
                ],
                cwd=project,
                env=env,
                timeout=120,
            )
        )
        self.assertFalse(installed.exists())


def markdown_links(root: Path) -> list[Path]:
    import re

    links: list[Path] = []
    for markdown in root.rglob("*.md"):
        for target in re.findall(
            r"\[[^\]]+\]\(([^)]+)\)",
            markdown.read_text(encoding="utf-8"),
        ):
            if "://" in target or target.startswith("#"):
                continue
            links.append((markdown.parent / target.split("#", 1)[0]).resolve())
    return links


def resolve_inside(root: Path, relative: object) -> Path:
    if not isinstance(relative, str):
        raise AssertionError("manifest path must be a string")
    candidate = (root / relative).resolve()
    if not candidate.is_relative_to(root.resolve()):
        raise AssertionError(f"path escapes plugin root: {relative}")
    return candidate


def smoke_environment(home: Path, executable_directory: Path) -> dict[str, str]:
    env = os.environ.copy()
    codex_home = home / ".codex"
    codex_home.mkdir(parents=True, exist_ok=True)
    env["HOME"] = str(home)
    env["CODEX_HOME"] = str(codex_home)
    env["PATH"] = f"{executable_directory}{os.pathsep}{env.get('PATH', '')}"
    return env


def assert_installed_skill(plugin_root: Path) -> None:
    skill = (
        plugin_root
        if (plugin_root / "SKILL.md").is_file()
        else plugin_root / "skills" / "kado-search"
    )
    self_contained = list(plugin_root.rglob("SKILL.md"))
    if self_contained != [skill / "SKILL.md"]:
        raise AssertionError(f"unexpected installed skills: {self_contained}")
    content = (skill / "SKILL.md").read_text(encoding="utf-8")
    if "Use the installed `kado` CLI" not in content:
        raise AssertionError("installed skill lost its CLI compatibility requirement")
    if "https://kado.so/install" not in content:
        raise AssertionError("installed skill lost its canonical install URL")


def successful(result: subprocess.CompletedProcess[str]) -> str:
    if result.returncode != 0:
        raise AssertionError(result.stdout + result.stderr)
    return result.stdout


def run(
    command: list[str],
    *,
    cwd: Path = REPOSITORY,
    env: dict[str, str] | None = None,
    timeout: int = 60,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=cwd,
        env=env,
        check=False,
        capture_output=True,
        text=True,
        timeout=timeout,
    )


if __name__ == "__main__":
    unittest.main()
