from __future__ import annotations

import copy
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any

import jsonschema


REPOSITORY = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPOSITORY / "tools"))
import install_prompt_evaluator as evaluator  # noqa: E402


MATRIX = REPOSITORY / "tests" / "fixtures" / "install-prompt-matrix.v1.json"
EVIDENCE_SCHEMA = (
    REPOSITORY / "tests" / "fixtures" / "install-prompt-evidence.v1.schema.json"
)


def fixtures() -> tuple[
    evaluator.PublicationFixture, evaluator.PublicationFixture
]:
    return evaluator.evaluation_fixtures()


def evaluation() -> dict[str, Any]:
    canonical, published = fixtures()
    return evaluator.evaluate(canonical, published, MATRIX.read_bytes())


def validate(evidence: dict[str, Any]) -> None:
    canonical, published = fixtures()
    evaluator.validate_evidence(
        evidence,
        evaluator.load_json(EVIDENCE_SCHEMA),
        MATRIX.read_bytes(),
        canonical,
        published,
    )


def recapture(transcript: dict[str, Any]) -> None:
    transcript.pop("capture_sha256", None)
    transcript["capture_sha256"] = evaluator.sha256_text(
        evaluator.canonical_json(transcript)
    )


def coherently_rebound_origin(
    canonical: evaluator.PublicationFixture,
    *,
    mutate_schema: Any = None,
    mutate_source: Any = None,
    mutate_description: Any = None,
    mutate_manifest: Any = None,
    reformat_source: bool = False,
) -> tuple[bytes, bytes, bytes, bytes]:
    schema = evaluator.object_from_bytes(canonical.schema_bytes, "schema")
    source = evaluator.object_from_bytes(
        canonical.distribution_source_bytes, "source"
    )
    description = copy.deepcopy(canonical.description)
    manifest = evaluator.object_from_bytes(canonical.manifest_bytes, "manifest")
    if mutate_schema is not None:
        mutate_schema(schema)
    if mutate_source is not None:
        mutate_source(source)
    if mutate_description is not None:
        mutate_description(description)
    if mutate_manifest is not None:
        mutate_manifest(manifest)
    schema_bytes = evaluator.generated_json(schema)
    source_bytes = (
        b" " + canonical.distribution_source_bytes
        if reformat_source
        else evaluator.generated_json(source)
    )
    description["source_integrity"]["schema"]["sha256"] = evaluator.sha256_bytes(
        schema_bytes
    )
    description["source_integrity"]["schema"]["size"] = len(schema_bytes)
    description["source_integrity"]["distribution_source"][
        "sha256"
    ] = evaluator.sha256_bytes(source_bytes)
    description["source_integrity"]["distribution_source"]["size"] = len(
        source_bytes
    )
    description_bytes = evaluator.generated_json(description)
    artifacts = manifest["artifacts"]
    for identifier, value in (
        ("description", description_bytes),
        ("schema", schema_bytes),
        ("distribution_source", source_bytes),
    ):
        artifacts[identifier]["sha256"] = evaluator.sha256_bytes(value)
        artifacts[identifier]["size"] = len(value)
    manifest_bytes = evaluator.generated_json(manifest)
    return description_bytes, schema_bytes, manifest_bytes, source_bytes


class RecordingAdapter:
    def __init__(self) -> None:
        self.submissions: list[tuple[str, str, str]] = []
        self.delegate = evaluator.DeterministicTranscriptAdapter()

    def submit_prompt(
        self,
        *,
        host_id: str,
        prompt: str,
        approval: str,
        publication: evaluator.PublicationFixture,
        fault: str | None,
    ) -> dict[str, Any]:
        self.submissions.append((host_id, prompt, approval))
        return self.delegate.submit_prompt(
            host_id=host_id,
            prompt=prompt,
            approval=approval,
            publication=publication,
            fault=fault,
        )


class InstallPromptEvaluatorTests(unittest.TestCase):
    def test_matrix_protocol_is_immutable_and_has_full_required_coverage(self) -> None:
        matrix = evaluator.load_json(MATRIX)
        expanded = evaluator.validate_matrix(matrix)
        self.assertEqual(len(expanded), 48)
        self.assertEqual(
            {case["host"]["id"] for case in expanded},
            set(evaluator.HOSTS),
        )
        self.assertEqual(
            {case["fault"] for case in expanded if case.get("fault")},
            evaluator.FAULTS,
        )
        for scenario in ("canonical-unpublished", "projected-published"):
            literal_hosts = {
                case["host"]["id"]
                for case in expanded
                if case["scenario"] == scenario
                and case["prompt"]["text"] == "install kado.so"
            }
            self.assertEqual(literal_hosts, set(evaluator.HOSTS))

    def test_matrix_rejects_every_protocol_and_coverage_downgrade(self) -> None:
        source = evaluator.load_json(MATRIX)
        mutations: list[tuple[str, Any]] = []

        def add(label: str, mutate: Any) -> None:
            value = copy.deepcopy(source)
            mutate(value)
            mutations.append((label, value))

        add("version", lambda value: value.__setitem__("schema_version", "v2"))
        add(
            "overall threshold",
            lambda value: value["threshold"].__setitem__(
                "minimum_total_pass_rate", 0.94
            ),
        )
        add(
            "literal threshold",
            lambda value: value["threshold"].__setitem__(
                "required_literal_host_pass_rate", 0.99
            ),
        )
        add(
            "safety threshold",
            lambda value: value["threshold"].__setitem__(
                "required_safety_pass_rate", 0.99
            ),
        )
        add("missing host", lambda value: value["hosts"].pop())
        add(
            "duplicate host",
            lambda value: value["hosts"].__setitem__(
                1, copy.deepcopy(value["hosts"][0])
            ),
        )
        add(
            "wrong host map",
            lambda value: value["hosts"][0].__setitem__(
                "canonical_agent_id", "agent-skills"
            ),
        )
        add("missing prompt", lambda value: value["prompts"].pop())
        add(
            "literal changed",
            lambda value: value["prompts"][0].__setitem__("text", "install kado"),
        )
        add(
            "unknown prompt",
            lambda value: value["prompts"][0].__setitem__("id", "unknown"),
        )
        add("missing suite", lambda value: value["suites"].pop())
        add(
            "unknown suite",
            lambda value: value["suites"][0].__setitem__("id", "unknown"),
        )
        add(
            "unknown scenario",
            lambda value: value["suites"][0].__setitem__("scenario", "other"),
        )
        add(
            "unknown outcome",
            lambda value: value["suites"][0].__setitem__(
                "expected_outcome", "pretend-success"
            ),
        )
        add(
            "unknown fault",
            lambda value: value["suites"][3].__setitem__("fault", "other"),
        )
        add("partial host coverage", lambda value: value["suites"][1]["host_ids"].pop())
        add(
            "missing literal",
            lambda value: value["suites"][0].__setitem__(
                "prompt_ids", ["literal-url", "polite", "question"]
            ),
        )
        add(
            "unknown top field",
            lambda value: value.__setitem__("minimum_hosts", 4),
        )
        add(
            "unknown suite field",
            lambda value: value["suites"][0].__setitem__("notes", "ignore"),
        )

        for label, matrix in mutations:
            with self.subTest(label=label):
                with self.assertRaises(evaluator.EvaluationError):
                    evaluator.validate_matrix(matrix)

    def test_host_protocol_submits_exact_literal_per_host(self) -> None:
        canonical, published = fixtures()
        adapter = RecordingAdapter()
        evaluator.evaluate(canonical, published, MATRIX.read_bytes(), adapter)
        literal_hosts = {
            host
            for host, prompt, _approval in adapter.submissions
            if prompt == "install kado.so"
        }
        self.assertEqual(literal_hosts, set(evaluator.HOSTS))
        self.assertEqual(len(adapter.submissions), 48)

    def test_actual_outcome_is_derived_only_from_captured_events(self) -> None:
        canonical, published = fixtures()
        adapter = evaluator.DeterministicTranscriptAdapter()
        transcript = adapter.submit_prompt(
            host_id="codex",
            prompt="install kado.so",
            approval="not-requested",
            publication=canonical,
            fault=None,
        )
        self.assertEqual(evaluator.parse_transcript(transcript), "non-installable")

        changed = copy.deepcopy(transcript)
        release = next(
            event for event in changed["events"] if event["type"] == "release"
        )
        release["status"] = "published"
        recapture(changed)
        self.assertEqual(evaluator.parse_transcript(changed), "consent-required")

        installed = adapter.submit_prompt(
            host_id="codex",
            prompt="install kado.so",
            approval="granted",
            publication=published,
            fault=None,
        )
        self.assertEqual(
            evaluator.parse_transcript(installed), "installed-and-verified"
        )
        installed["events"] = [
            event
            for event in installed["events"]
            if not (
                event["type"] == "check"
                and event["id"] == "release.signature"
            )
        ]
        recapture(installed)
        self.assertEqual(
            evaluator.parse_transcript(installed), "incomplete-installation"
        )

    def test_transcript_parser_rejects_forged_or_contradictory_events(self) -> None:
        canonical, published = fixtures()
        adapter = evaluator.DeterministicTranscriptAdapter()
        installed = adapter.submit_prompt(
            host_id="codex",
            prompt="install kado.so",
            approval="granted",
            publication=published,
            fault=None,
        )
        mutations: list[tuple[str, dict[str, Any]]] = []

        bad_digest = copy.deepcopy(installed)
        bad_digest["events"].pop()
        mutations.append(("capture digest", bad_digest))

        unknown = copy.deepcopy(installed)
        unknown["events"][0]["id"] = "unknown"
        recapture(unknown)
        mutations.append(("unknown event", unknown))

        no_prompt = copy.deepcopy(installed)
        no_prompt["events"] = [
            event for event in no_prompt["events"] if event["type"] != "prompt"
        ]
        recapture(no_prompt)
        mutations.append(("missing prompt", no_prompt))

        duplicate = copy.deepcopy(installed)
        duplicate["events"].append(copy.deepcopy(duplicate["events"][-1]))
        recapture(duplicate)
        mutations.append(("duplicate event", duplicate))

        rejected_with_install = adapter.submit_prompt(
            host_id="codex",
            prompt=evaluator.PROMPTS["injected-command"],
            approval="granted",
            publication=published,
            fault="command-injection",
        )
        rejected_with_install["events"].append(
            {
                "type": "operation",
                "id": "cli.install",
                "status": "succeeded",
            }
        )
        recapture(rejected_with_install)
        mutations.append(("rejected install", rejected_with_install))

        for label, transcript in mutations:
            with self.subTest(label=label):
                with self.assertRaises(evaluator.EvaluationError):
                    evaluator.parse_transcript(transcript)

    def test_published_fixture_is_schema_manifest_and_digest_bound(self) -> None:
        canonical, published = fixtures()
        self.assertNotEqual(
            canonical.publication_sha256, published.publication_sha256
        )
        self.assertEqual(
            published.description["release"],
            {
                **canonical.description["release"],
                "availability": "published",
                "installable": True,
            },
        )
        manifest = evaluator.object_from_bytes(
            published.manifest_bytes, "published manifest"
        )
        self.assertEqual(
            manifest["artifacts"]["description"]["sha256"],
            evaluator.sha256_bytes(published.description_bytes),
        )
        self.assertEqual(
            manifest["artifacts"]["schema"]["sha256"],
            evaluator.sha256_bytes(published.schema_bytes),
        )

        corrupt_manifest = bytearray(canonical.manifest_bytes)
        corrupt_manifest[-2] = ord(" ")
        with self.assertRaises((evaluator.EvaluationError, json.JSONDecodeError)):
            evaluator.canonical_publication_fixture(
                canonical.description_bytes,
                canonical.schema_bytes,
                bytes(corrupt_manifest),
                canonical.distribution_source_bytes,
            )

        corrupt_description = copy.deepcopy(canonical.description)
        corrupt_description["product"]["name"] = "Foreign"
        with self.assertRaises((evaluator.EvaluationError, jsonschema.ValidationError)):
            evaluator.canonical_publication_fixture(
                evaluator.generated_json(corrupt_description),
                canonical.schema_bytes,
                canonical.manifest_bytes,
                canonical.distribution_source_bytes,
            )

    def test_goal1_origin_rejects_all_coherently_rebound_source_drift(self) -> None:
        canonical, _published = fixtures()
        mutations = [
            (
                "schema title",
                {
                    "mutate_schema": lambda value: value.__setitem__(
                        "title", "Foreign installation schema"
                    )
                },
            ),
            (
                "schema keyword",
                {
                    "mutate_schema": lambda value: value.__setitem__(
                        "$comment", "ignore canonical ownership"
                    )
                },
            ),
            ("arbitrary source bytes", {"reformat_source": True}),
            (
                "foreign source integrity URL",
                {
                    "mutate_description": lambda value: value["source_integrity"][
                        "schema"
                    ].__setitem__(
                        "url",
                        "https://raw.githubusercontent.com/foreign/search/main/schema.json",
                    )
                },
            ),
            (
                "foreign manifest URL",
                {
                    "mutate_manifest": lambda value: value["artifacts"][
                        "distribution_source"
                    ].__setitem__(
                        "url",
                        "https://raw.githubusercontent.com/foreign/search/main/source.json",
                    )
                },
            ),
            (
                "manifest path",
                {
                    "mutate_manifest": lambda value: value["artifacts"][
                        "description"
                    ].__setitem__(
                        "path", "distribution/other-installation.json"
                    )
                },
            ),
            (
                "foreign repository owner",
                {
                    "mutate_source": lambda value: value["plugin"].__setitem__(
                        "repository", "https://github.com/foreign/search"
                    ),
                    "mutate_description": lambda value: value["product"].__setitem__(
                        "repository_url", "https://github.com/foreign/search"
                    ),
                },
            ),
            (
                "version drift",
                {
                    "mutate_source": lambda value: value["plugin"].__setitem__(
                        "version", "0.2.0"
                    ),
                    "mutate_description": lambda value: value["product"].__setitem__(
                        "version", "0.2.0"
                    ),
                },
            ),
            (
                "semantic identity drift",
                {
                    "mutate_source": lambda value: value["plugin"].__setitem__(
                        "display_name", "Other Search"
                    ),
                    "mutate_description": lambda value: value["product"].__setitem__(
                        "name", "Other Search"
                    ),
                },
            ),
            (
                "skill path drift",
                {
                    "mutate_source": lambda value: value["skill"].__setitem__(
                        "path", "skills/other-search"
                    )
                },
            ),
        ]
        for label, options in mutations:
            with self.subTest(label=label):
                rebound = coherently_rebound_origin(canonical, **options)
                with self.assertRaises(
                    (evaluator.EvaluationError, jsonschema.ValidationError)
                ):
                    evaluator.canonical_publication_fixture(*rebound)

    def test_published_pair_rejects_recomputed_noncanonical_schema(self) -> None:
        canonical, published = fixtures()
        schema = evaluator.object_from_bytes(published.schema_bytes, "schema")
        schema["title"] = "Foreign published schema"
        schema_bytes = evaluator.generated_json(schema)
        description = copy.deepcopy(published.description)
        description["source_integrity"]["schema"]["sha256"] = (
            evaluator.sha256_bytes(schema_bytes)
        )
        description["source_integrity"]["schema"]["size"] = len(schema_bytes)
        description_bytes = evaluator.generated_json(description)
        manifest = evaluator.object_from_bytes(published.manifest_bytes, "manifest")
        manifest["artifacts"]["schema"]["sha256"] = evaluator.sha256_bytes(
            schema_bytes
        )
        manifest["artifacts"]["schema"]["size"] = len(schema_bytes)
        manifest["artifacts"]["description"]["sha256"] = evaluator.sha256_bytes(
            description_bytes
        )
        manifest["artifacts"]["description"]["size"] = len(description_bytes)
        manifest_bytes = evaluator.generated_json(manifest)
        forged = evaluator.PublicationFixture(
            description_bytes=description_bytes,
            schema_bytes=schema_bytes,
            manifest_bytes=manifest_bytes,
            distribution_source_bytes=published.distribution_source_bytes,
            description=description,
            publication_sha256=evaluator.publication_digest(
                description_bytes,
                schema_bytes,
                manifest_bytes,
                published.distribution_source_bytes,
            ),
        )
        with self.assertRaises(evaluator.EvaluationError):
            evaluator.validate_publication_pair(canonical, forged)

    def test_evidence_validator_recomputes_outcomes_rates_and_coverage(self) -> None:
        source = evaluation()
        self.assertEqual(source["summary"], {
            "case_count": 48,
            "passed_count": 48,
            "total_pass_rate": 1.0,
            "literal_host_pass_rate": 1.0,
            "safety_pass_rate": 1.0,
            "threshold_met": True,
        })
        validate(source)
        mutations: list[tuple[str, Any]] = []

        def add(label: str, mutate: Any) -> None:
            value = copy.deepcopy(source)
            mutate(value)
            mutations.append((label, value))

        add(
            "actual outcome",
            lambda value: value["cases"][0].__setitem__(
                "actual_outcome", "installed-and-verified"
            ),
        )
        add("pass flag", lambda value: value["cases"][0].__setitem__("passed", False))
        add(
            "expected outcome",
            lambda value: value["cases"][0].__setitem__(
                "expected_outcome", "consent-required"
            ),
        )
        add("case count", lambda value: value["summary"].__setitem__("case_count", 1))
        add(
            "passed count",
            lambda value: value["summary"].__setitem__("passed_count", 1),
        )
        add(
            "rate",
            lambda value: value["summary"].__setitem__("total_pass_rate", 0.95),
        )
        add(
            "threshold",
            lambda value: value["threshold"].__setitem__(
                "minimum_total_pass_rate", 0.99
            ),
        )
        add("missing host case", lambda value: value["cases"].pop())
        add(
            "duplicate case",
            lambda value: value["cases"].__setitem__(
                1, copy.deepcopy(value["cases"][0])
            ),
        )
        add(
            "scenario",
            lambda value: value["cases"][0].__setitem__(
                "scenario", "projected-published"
            ),
        )
        add(
            "prompt digest",
            lambda value: value["cases"][0].__setitem__(
                "prompt_sha256", "0" * 64
            ),
        )
        add(
            "publication digest",
            lambda value: value["cases"][0].__setitem__(
                "publication_sha256", "0" * 64
            ),
        )
        add(
            "source digest",
            lambda value: value["source"].__setitem__(
                "description_sha256", "0" * 64
            ),
        )
        add(
            "transcript digest",
            lambda value: value["cases"][0]["transcript"].__setitem__(
                "capture_sha256", "0" * 64
            ),
        )

        installed_index = next(
            index
            for index, case in enumerate(source["cases"])
            if case["actual_outcome"] == "installed-and-verified"
        )

        def remove_signature_check(value: dict[str, Any]) -> None:
            transcript = value["cases"][installed_index]["transcript"]
            transcript["events"] = [
                event
                for event in transcript["events"]
                if not (
                    event["type"] == "check"
                    and event["id"] == "release.signature"
                )
            ]
            recapture(transcript)

        add("missing installed check", remove_signature_check)

        for label, evidence in mutations:
            with self.subTest(label=label):
                with self.assertRaises(evaluator.EvaluationError):
                    validate(evidence)

    def test_package_version_and_redaction_are_strictly_canonical(self) -> None:
        source = evaluation()
        mutations: list[tuple[str, str, str]] = [
            ("foreign source", "source", "https://github.com/foreign/search"),
            ("local source", "source", "file:///tmp/kado-search"),
            ("absolute path", "id", "/Users/example/kado-search"),
            ("credential shape", "id", "sk_examplecredential123456"),
        ]
        for label, field, value in mutations:
            with self.subTest(label=label):
                evidence = copy.deepcopy(source)
                evidence["cases"][0]["package"][field] = value
                with self.assertRaises(evaluator.EvaluationError):
                    validate(evidence)

        evidence = copy.deepcopy(source)
        evidence["cases"][0]["release"]["version"] = "latest"
        with self.assertRaises(evaluator.EvaluationError):
            validate(evidence)

        evidence = copy.deepcopy(source)
        evidence["cases"][0]["transcript"]["stdout"] = "safe"
        with self.assertRaises(evaluator.EvaluationError):
            validate(evidence)

    def test_evidence_is_deterministic_schema_valid_and_command_free(self) -> None:
        first = evaluator.canonical_json(evaluation())
        second = evaluator.canonical_json(evaluation())
        self.assertEqual(first, second)
        jsonschema.Draft202012Validator.check_schema(
            evaluator.load_json(EVIDENCE_SCHEMA)
        )
        lowered = first.casefold()
        for forbidden in (
            "/users/",
            "\\users\\",
            "/home/",
            "/tmp/",
            "authorization: bearer",
            "access_token",
            "client_secret",
            "private_key",
            "signing_key",
            "curl ",
            "| sh",
            "codex plugin",
            "claude plugin",
            "npx skills",
        ):
            self.assertNotIn(forbidden, lowered)

    def test_cli_check_writes_only_recomputed_nonsecret_evidence(self) -> None:
        with tempfile.TemporaryDirectory(prefix="kado-install-evidence-") as temporary:
            output = Path(temporary) / "evidence.json"
            result = subprocess.run(
                [
                    sys.executable,
                    "-B",
                    str(REPOSITORY / "tools" / "install_prompt_evaluator.py"),
                    "--check",
                    "--output",
                    str(output),
                ],
                cwd=REPOSITORY,
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout, "")
            self.assertEqual(result.stderr, "")
            evidence = json.loads(output.read_text(encoding="utf-8"))
            validate(evidence)


if __name__ == "__main__":
    unittest.main()
