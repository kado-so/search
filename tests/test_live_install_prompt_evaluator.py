from __future__ import annotations

import copy
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


REPOSITORY = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPOSITORY / "tools"))
import install_prompt_evaluator as protocol  # noqa: E402
import live_install_prompt_evaluator as live  # noqa: E402


class LiveInstallPromptEvaluatorTests(unittest.TestCase):
    def test_live_adapters_submit_exact_prompt_in_deny_by_default_isolation(self) -> None:
        commands: list[list[str]] = []
        environments: list[dict[str, str]] = []

        def runner(
            command: list[str],
            **options: object,
        ) -> subprocess.CompletedProcess[str]:
            commands.append(command)
            environments.append(options["env"])  # type: ignore[arg-type]
            if command[0] == "/tools/codex":
                return subprocess.CompletedProcess(
                    command,
                    0,
                    stdout=(
                        "Kado is unpublished and not currently installable. "
                        "Review https://kado.so/install."
                    ),
                    stderr="",
                )
            return subprocess.CompletedProcess(
                command,
                1,
                stdout="",
                stderr="Authentication required; API key is unavailable.",
            )

        report = live.evaluate_live_hosts(
            runner=runner,
            executables={
                "codex": "/tools/codex",
                "claude-code": "/tools/claude",
                "agent-skills": "/tools/npx",
            },
        )

        self.assertEqual(len(commands), 2)
        self.assertTrue(all(command[-1] == "install kado.so" for command in commands))
        self.assertIn("read-only", commands[0])
        self.assertNotIn("--dangerously-bypass-approvals-and-sandbox", commands[0])
        self.assertIn("--permission-mode", commands[1])
        self.assertEqual(commands[1][commands[1].index("--permission-mode") + 1], "plan")
        self.assertEqual(commands[1][commands[1].index("--tools") + 1], "")
        for environment in environments:
            self.assertNotEqual(environment["HOME"], os.environ.get("HOME"))
            self.assertTrue(environment["CODEX_HOME"].endswith("/.codex"))

        attempts = {attempt["host_id"]: attempt for attempt in report["attempts"]}
        self.assertEqual(attempts["codex"]["availability"], "evaluated")
        self.assertEqual(
            attempts["codex"]["parsed_outcome"], "non-installable"
        )
        self.assertEqual(
            attempts["claude-code"]["availability"],
            "authentication-unavailable",
        )
        self.assertEqual(
            attempts["claude-code"]["parsed_outcome"],
            "discovery-unavailable",
        )
        self.assertEqual(
            attempts["agent-skills"]["availability"],
            "unsupported-prompt-interface",
        )
        self.assertFalse(attempts["agent-skills"]["prompt_submitted"])
        self.assertFalse(
            any(
                event["type"] == "prompt"
                for event in attempts["agent-skills"]["transcript"]["events"]
            )
        )
        self.assertEqual(report["live_gate"], {
            "met": False,
            "required_hosts": ["agent-skills", "claude-code", "codex"],
            "successful_hosts": [],
            "correctness_failures": ["codex"],
        })
        self.assertEqual(
            report["captured_failures"][0]["parsed_outcome"],
            "partial-installation",
        )
        self.assertEqual(
            report["captured_failures"][0]["approval_mode"],
            "imperative-consent",
        )
        self.assertTrue(
            report["captured_failures"][0]["cleanup_completed"]
        )
        for attempt in attempts.values():
            self.assertEqual(
                protocol.parse_transcript(attempt["transcript"]),
                attempt["parsed_outcome"],
            )

        encoded = protocol.canonical_json(report).casefold()
        self.assertNotIn("authentication required", encoded)
        self.assertNotIn("api key is unavailable", encoded)
        self.assertNotIn("/tools/", encoded)
        self.assertNotIn("/tmp/", encoded)
        self.assertNotIn("/users/", encoded)

    def test_live_adapter_rejects_response_that_suggests_install_commands(self) -> None:
        def runner(
            command: list[str],
            **_options: object,
        ) -> subprocess.CompletedProcess[str]:
            return subprocess.CompletedProcess(
                command,
                0,
                stdout=(
                    "Kado is unpublished and not currently installable. "
                    "Review https://kado.so/install then run npx skills add."
                ),
                stderr="",
            )

        report = live.evaluate_live_hosts(
            runner=runner,
            executables={
                "codex": "/tools/codex",
                "claude-code": None,
                "agent-skills": None,
            },
        )
        codex = report["attempts"][0]
        self.assertEqual(codex["availability"], "response-unusable")
        self.assertEqual(codex["parsed_outcome"], "discovery-unavailable")

    def test_live_cli_is_explicitly_gated(self) -> None:
        with tempfile.TemporaryDirectory(prefix="kado-live-gate-") as temporary:
            output = Path(temporary) / "evidence.json"
            environment = os.environ.copy()
            environment.pop("KADO_INSTALL_PROMPT_LIVE", None)
            result = subprocess.run(
                [
                    sys.executable,
                    "-B",
                    str(REPOSITORY / "tools" / "live_install_prompt_evaluator.py"),
                    "--output",
                    str(output),
                ],
                cwd=REPOSITORY,
                env=environment,
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output.exists())
            self.assertIn("KADO_INSTALL_PROMPT_LIVE=1", result.stderr)

    def test_live_report_redaction_rejects_raw_output_fields(self) -> None:
        report = {
            "schema_version": live.LIVE_VERSION,
            "attempts": [{"stdout": "safe"}],
        }
        with self.assertRaises(protocol.EvaluationError):
            protocol._validate_redaction(report)

    def test_live_report_validator_recomputes_transcripts_and_host_coverage(self) -> None:
        report = live.evaluate_live_hosts(
            executables={
                "codex": None,
                "claude-code": None,
                "agent-skills": None,
            }
        )
        canonical, _published = protocol.evaluation_fixtures()
        live.validate_live_report(report, canonical)

        outcome = copy.deepcopy(report)
        outcome["attempts"][0]["parsed_outcome"] = "non-installable"
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(outcome, canonical)

        coverage = copy.deepcopy(report)
        coverage["attempts"].pop()
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(coverage, canonical)

        safety = copy.deepcopy(report)
        safety["safety"]["isolated_approval_granted"] = True
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(safety, canonical)

    def test_live_availability_labels_are_exactly_transcript_derived(self) -> None:
        def runner(
            command: list[str],
            **_options: object,
        ) -> subprocess.CompletedProcess[str]:
            if command[0] == "/tools/codex":
                return subprocess.CompletedProcess(
                    command,
                    0,
                    stdout=(
                        "Kado is unpublished and not currently installable. "
                        "Review https://kado.so/install."
                    ),
                    stderr="",
                )
            return subprocess.CompletedProcess(
                command,
                1,
                stdout="",
                stderr="Authentication required.",
            )

        report = live.evaluate_live_hosts(
            runner=runner,
            executables={
                "codex": "/tools/codex",
                "claude-code": "/tools/claude",
                "agent-skills": "/tools/npx",
            },
        )
        canonical, _published = protocol.evaluation_fixtures()
        attempts = {attempt["host_id"]: attempt for attempt in report["attempts"]}
        relabels = [
            ("evaluated as response", "codex", "response-unusable"),
            ("auth as process", "claude-code", "process-unavailable"),
            (
                "unsupported as missing tool",
                "agent-skills",
                "tool-unavailable",
            ),
        ]
        for label, host_id, availability in relabels:
            with self.subTest(label=label):
                forged = copy.deepcopy(report)
                target = next(
                    attempt
                    for attempt in forged["attempts"]
                    if attempt["host_id"] == host_id
                )
                target["availability"] = availability
                with self.assertRaises(protocol.EvaluationError):
                    live.validate_live_report(forged, canonical)

        prompt_flag = copy.deepcopy(report)
        prompt_flag["attempts"][0]["prompt_submitted"] = False
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(prompt_flag, canonical)

        false_submission_event = copy.deepcopy(report)
        unsupported = next(
            attempt
            for attempt in false_submission_event["attempts"]
            if attempt["host_id"] == "agent-skills"
        )
        unsupported["transcript"]["events"].insert(
            0,
            {
                "type": "prompt",
                "id": "literal-install",
                "status": "submitted",
            },
        )
        unsupported["transcript"].pop("capture_sha256")
        unsupported["transcript"]["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(unsupported["transcript"])
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(false_submission_event, canonical)

        missing = live.evaluate_live_hosts(
            executables={
                "codex": None,
                "claude-code": None,
                "agent-skills": None,
            }
        )
        self.assertTrue(
            all(
                not any(
                    event["type"] == "prompt"
                    for event in attempt["transcript"]["events"]
                )
                for attempt in missing["attempts"]
            )
        )
        missing["attempts"][0]["availability"] = "authentication-unavailable"
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(missing, canonical)

        forged_gate = copy.deepcopy(report)
        forged_gate["live_gate"]["met"] = True
        forged_gate["live_gate"]["successful_hosts"] = [
            "agent-skills",
            "claude-code",
            "codex",
        ]
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(forged_gate, canonical)

        deterministic_attempt = copy.deepcopy(attempts["codex"])
        deterministic_attempt["transcript"]["capture_kind"] = (
            "deterministic-fixture"
        )
        self.assertEqual(
            live._live_gate([deterministic_attempt], []),
            {
                "met": False,
                "required_hosts": ["agent-skills", "claude-code", "codex"],
                "successful_hosts": [],
                "correctness_failures": [],
            },
        )

    def test_captured_partial_installation_is_exact_and_never_meets_gate(self) -> None:
        report = live.evaluate_live_hosts(
            executables={
                "codex": None,
                "claude-code": None,
                "agent-skills": None,
            }
        )
        canonical, _published = protocol.evaluation_fixtures()
        captured = report["captured_failures"][0]
        self.assertEqual(captured["availability"], "partial-installation")
        self.assertEqual(captured["parsed_outcome"], "partial-installation")
        self.assertTrue(captured["prompt_submitted"])
        self.assertTrue(captured["tool_available"])
        self.assertEqual(captured["approval_mode"], "imperative-consent")
        self.assertTrue(captured["cleanup_completed"])
        self.assertFalse(report["live_gate"]["met"])
        self.assertEqual(report["live_gate"]["correctness_failures"], ["codex"])
        self.assertEqual(report["live_gate"]["successful_hosts"], [])

        relabeled = copy.deepcopy(report)
        relabeled["captured_failures"][0]["availability"] = (
            "installed-and-verified"
        )
        relabeled["captured_failures"][0]["parsed_outcome"] = (
            "installed-and-verified"
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(relabeled, canonical)

        consent = copy.deepcopy(report)
        consent["captured_failures"][0]["approval_mode"] = "without-approval"
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(consent, canonical)

        no_cleanup = copy.deepcopy(report)
        transcript = no_cleanup["captured_failures"][0]["transcript"]
        transcript["events"] = [
            event for event in transcript["events"]
            if event["type"] != "cleanup"
        ]
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(no_cleanup, canonical)

        false_cleanup_label = copy.deepcopy(report)
        false_cleanup_label["captured_failures"][0]["cleanup_completed"] = False
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(false_cleanup_label, canonical)

        requested_cleanup = copy.deepcopy(report)
        transcript = requested_cleanup["captured_failures"][0]["transcript"]
        removal = next(
            event
            for event in transcript["events"]
            if event["type"] == "cleanup" and event["id"] == "package.remove"
        )
        removal["status"] = "requested"
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(requested_cleanup, canonical)

        unverified_absence = copy.deepcopy(report)
        transcript = unverified_absence["captured_failures"][0]["transcript"]
        transcript["events"] = [
            event
            for event in transcript["events"]
            if not (
                event["type"] == "cleanup"
                and event["id"] == "package.absent"
            )
        ]
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(unverified_absence, canonical)

        reordered = copy.deepcopy(report)
        transcript = reordered["captured_failures"][0]["transcript"]
        transcript["events"][1], transcript["events"][2] = (
            transcript["events"][2],
            transcript["events"][1],
        )
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(reordered, canonical)

        duplicate = copy.deepcopy(report)
        transcript = duplicate["captured_failures"][0]["transcript"]
        transcript["events"].insert(3, copy.deepcopy(transcript["events"][2]))
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(duplicate, canonical)

        extra = copy.deepcopy(report)
        transcript = extra["captured_failures"][0]["transcript"]
        transcript["events"].insert(
            1,
            {
                "type": "approval",
                "id": "install",
                "status": "requested",
            },
        )
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(extra, canonical)

        impossible_timestamps = copy.deepcopy(report)
        transcript = impossible_timestamps["captured_failures"][0]["transcript"]
        transcript["events"][0]["observed_at"] = "2026-07-24T00:00:02Z"
        transcript["events"][1]["observed_at"] = "2026-07-24T00:00:01Z"
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(impossible_timestamps, canonical)

        ambiguous_prompt = copy.deepcopy(report)
        transcript = ambiguous_prompt["captured_failures"][0]["transcript"]
        transcript["prompt_sha256"] = protocol.sha256_text(
            "Can you install kado.so?"
        )
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(ambiguous_prompt, canonical)

        replay = copy.deepcopy(report)
        transcript = replay["captured_failures"][0]["transcript"]
        transcript["capture_kind"] = "deterministic-fixture"
        transcript.pop("capture_sha256")
        transcript["capture_sha256"] = protocol.sha256_text(
            protocol.canonical_json(transcript)
        )
        with self.assertRaises(protocol.EvaluationError):
            live.validate_live_report(replay, canonical)


if __name__ == "__main__":
    unittest.main()
