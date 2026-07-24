#!/usr/bin/env python3
"""Run gated, isolated, deny-by-default live install-prompt attempts."""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Callable

import install_prompt_evaluator as protocol


LIVE_VERSION = "kado.install-live-attempts.v1"
LITERAL_PROMPT = "install kado.so"
CAPTURED_CODEX_PARTIAL = (
    protocol.REPOSITORY
    / "tests"
    / "fixtures"
    / "install-live-codex-partial.v1.json"
)
Runner = Callable[..., subprocess.CompletedProcess[str]]


def _event(event_type: str, identifier: str, status: str) -> dict[str, str]:
    return {"type": event_type, "id": identifier, "status": status}


def _live_transcript(
    *,
    host_id: str,
    publication: protocol.PublicationFixture,
    events: list[dict[str, str]],
    response: str | None = None,
) -> dict[str, Any]:
    payload: dict[str, Any] = {
        "protocol_version": protocol.TRANSCRIPT_VERSION,
        "capture_kind": "live-isolated",
        "adapter_id": f"{host_id}.live-deny.v1",
        "host_id": host_id,
        "prompt_sha256": protocol.sha256_text(LITERAL_PROMPT),
        "approval_decision": "denied",
        "publication_sha256": publication.publication_sha256,
        "events": events,
    }
    if response is not None:
        payload["response_sha256"] = protocol.sha256_text(response)
    return protocol.capture_transcript(payload)


def _result_events(result: subprocess.CompletedProcess[str]) -> tuple[
    list[dict[str, str]], str
]:
    response = f"{result.stdout}\n{result.stderr}"
    lowered = response.casefold()
    base = [_event("prompt", "literal-install", "submitted")]
    authentication_failure = any(
        marker in lowered
        for marker in (
            "not logged in",
            "authentication",
            "api key",
            "auth.json",
            "login required",
            "unauthorized",
            "please run",
        )
    )
    if authentication_failure:
        return (
            base + [_event("adapter", "authentication", "unavailable")],
            response,
        )
    if result.returncode != 0:
        return (
            base + [_event("adapter", "process", "unavailable")],
            response,
        )
    canonical_signals = (
        "unpublished" in lowered
        and (
            "not installable" in lowered
            or "not currently installable" in lowered
        )
        and "kado.so/install" in lowered
    )
    command_signals = (
        "codex plugin add",
        "claude plugin install",
        "npx skills add",
        "curl ",
        "| sh",
    )
    if not canonical_signals or any(signal in lowered for signal in command_signals):
        return (
            base + [_event("adapter", "response-policy", "unavailable")],
            response,
        )
    return (
        base
        + [
            _event("discovery", "homepage", "ok"),
            _event("discovery", "llms", "ok"),
            _event("discovery", "install-description", "ok"),
            _event("metadata", "canonical-installation", "valid"),
            _event("release", "availability", "unpublished"),
        ],
        response,
    )


def _isolated_environment(home: Path) -> dict[str, str]:
    environment = os.environ.copy()
    environment["HOME"] = str(home)
    environment["CODEX_HOME"] = str(home / ".codex")
    environment["XDG_CONFIG_HOME"] = str(home / ".config")
    environment["XDG_CACHE_HOME"] = str(home / ".cache")
    environment["XDG_DATA_HOME"] = str(home / ".local" / "share")
    environment.pop("CLAUDE_CONFIG_DIR", None)
    return environment


def _attempt_agent(
    *,
    host_id: str,
    executable: str | None,
    command: list[str] | None,
    publication: protocol.PublicationFixture,
    runner: Runner,
    home: Path,
    workspace: Path,
) -> dict[str, Any]:
    home.mkdir(parents=True, exist_ok=True)
    if executable is None:
        transcript = _live_transcript(
            host_id=host_id,
            publication=publication,
            events=[
                _event("adapter", "tool", "unavailable"),
            ],
        )
        return _attempt_from_transcript(host_id, False, transcript)
    if command is None:
        transcript = _live_transcript(
            host_id=host_id,
            publication=publication,
            events=[
                _event("adapter", "prompt-interface", "unavailable"),
            ],
        )
        return _attempt_from_transcript(host_id, True, transcript)
    try:
        result = runner(
            command,
            cwd=workspace,
            env=_isolated_environment(home),
            check=False,
            capture_output=True,
            text=True,
            timeout=60,
        )
        events, response = _result_events(result)
        transcript = _live_transcript(
            host_id=host_id,
            publication=publication,
            events=events,
            response=response,
        )
        return _attempt_from_transcript(host_id, True, transcript)
    except (OSError, subprocess.TimeoutExpired):
        transcript = _live_transcript(
            host_id=host_id,
            publication=publication,
            events=[
                _event("prompt", "literal-install", "submitted"),
                _event("adapter", "process", "unavailable"),
            ],
        )
        return _attempt_from_transcript(host_id, True, transcript)


def _attempt_from_transcript(
    host_id: str,
    tool_available: bool,
    transcript: dict[str, Any],
) -> dict[str, Any]:
    outcome = protocol.parse_transcript(transcript)
    prompt_submitted = any(
        event
        == _event("prompt", "literal-install", "submitted")
        for event in transcript["events"]
    )
    adapter_reasons = [
        event["id"]
        for event in transcript["events"]
        if event["type"] == "adapter" and event["status"] == "unavailable"
    ]
    if not tool_available and adapter_reasons == ["tool"] and not prompt_submitted:
        availability = "tool-unavailable"
    elif (
        tool_available
        and adapter_reasons == ["prompt-interface"]
        and not prompt_submitted
    ):
        availability = "unsupported-prompt-interface"
    elif (
        tool_available
        and adapter_reasons == ["authentication"]
        and prompt_submitted
    ):
        availability = "authentication-unavailable"
    elif tool_available and adapter_reasons == ["process"] and prompt_submitted:
        availability = "process-unavailable"
    elif (
        tool_available
        and adapter_reasons == ["response-policy"]
        and prompt_submitted
    ):
        availability = "response-unusable"
    elif (
        tool_available
        and not adapter_reasons
        and prompt_submitted
        and outcome == "non-installable"
    ):
        availability = "evaluated"
    else:
        raise protocol.EvaluationError(
            "live availability cannot be derived from transcript events"
        )
    return {
        "host_id": host_id,
        "tool_available": tool_available,
        "prompt_submitted": prompt_submitted,
        "availability": availability,
        "approval_mode": "deny-only",
        "parsed_outcome": outcome,
        "transcript": transcript,
    }


def _captured_failure_from_transcript(
    transcript: dict[str, Any],
) -> dict[str, Any]:
    outcome = protocol.parse_transcript(transcript)
    if (
        transcript["capture_kind"] != "live-captured"
        or transcript["adapter_id"] != "codex.subagent-live.v1"
        or transcript["host_id"] != "codex"
        or transcript["prompt_sha256"] != protocol.sha256_text(LITERAL_PROMPT)
        or transcript["approval_decision"] != "granted"
        or outcome != "partial-installation"
    ):
        raise protocol.EvaluationError(
            "captured installation failure is not exact imperative-consent evidence"
        )
    return {
        "host_id": "codex",
        "tool_available": True,
        "prompt_submitted": True,
        "availability": "partial-installation",
        "approval_mode": "imperative-consent",
        "parsed_outcome": outcome,
        "cleanup_completed": True,
        "transcript": transcript,
    }


def _load_captured_failures(
    canonical: protocol.PublicationFixture,
) -> list[dict[str, Any]]:
    transcript = protocol.load_json(CAPTURED_CODEX_PARTIAL)
    if transcript.get("publication_sha256") != canonical.publication_sha256:
        raise protocol.EvaluationError(
            "captured installation failure publication binding drifted"
        )
    return [_captured_failure_from_transcript(transcript)]


def evaluate_live_hosts(
    *,
    runner: Runner = subprocess.run,
    executables: dict[str, str | None] | None = None,
) -> dict[str, Any]:
    canonical, _published = protocol.evaluation_fixtures()
    resolved = executables or {
        "codex": shutil.which("codex"),
        "claude-code": shutil.which("claude"),
        "agent-skills": shutil.which("npx"),
    }
    with tempfile.TemporaryDirectory(prefix="kado-live-install-") as temporary:
        root = Path(temporary)
        attempts: list[dict[str, Any]] = []
        codex = resolved.get("codex")
        attempts.append(
            _attempt_agent(
                host_id="codex",
                executable=codex,
                command=(
                    [
                        codex,
                        "exec",
                        "--ephemeral",
                        "--ignore-user-config",
                        "--ignore-rules",
                        "--skip-git-repo-check",
                        "--sandbox",
                        "read-only",
                        "--json",
                        "--cd",
                        str(root / "codex-workspace"),
                        LITERAL_PROMPT,
                    ]
                    if codex is not None
                    else None
                ),
                publication=canonical,
                runner=runner,
                home=root / "codex-home",
                workspace=_mkdir(root / "codex-workspace"),
            )
        )
        claude = resolved.get("claude-code")
        attempts.append(
            _attempt_agent(
                host_id="claude-code",
                executable=claude,
                command=(
                    [
                        claude,
                        "--print",
                        "--bare",
                        "--no-session-persistence",
                        "--permission-mode",
                        "plan",
                        "--tools",
                        "",
                        "--max-budget-usd",
                        "0.05",
                        "--output-format",
                        "json",
                        LITERAL_PROMPT,
                    ]
                    if claude is not None
                    else None
                ),
                publication=canonical,
                runner=runner,
                home=root / "claude-home",
                workspace=_mkdir(root / "claude-workspace"),
            )
        )
        attempts.append(
            _attempt_agent(
                host_id="agent-skills",
                executable=resolved.get("agent-skills"),
                command=None,
                publication=canonical,
                runner=runner,
                home=root / "skills-home",
                workspace=_mkdir(root / "skills-workspace"),
            )
        )
    captured_failures = _load_captured_failures(canonical)
    report = {
        "schema_version": LIVE_VERSION,
        "evaluation_mode": "live-isolated-deny",
        "prompt_sha256": protocol.sha256_text(LITERAL_PROMPT),
        "publication_sha256": canonical.publication_sha256,
        "safety": {
            "isolated_home": True,
            "isolated_tools_disabled_or_read_only": True,
            "isolated_approval_granted": False,
            "isolated_external_install": False,
            "captured_partial_installations": len(captured_failures),
            "captured_cleanup_completed": all(
                failure["cleanup_completed"] is True
                for failure in captured_failures
            ),
        },
        "attempts": attempts,
        "captured_failures": captured_failures,
        "live_gate": _live_gate(attempts, captured_failures),
    }
    validate_live_report(report, canonical)
    return report


def validate_live_report(
    report: dict[str, Any], canonical: protocol.PublicationFixture
) -> None:
    if set(report) != {
        "schema_version",
        "evaluation_mode",
        "prompt_sha256",
        "publication_sha256",
        "safety",
        "attempts",
        "captured_failures",
        "live_gate",
    }:
        raise protocol.EvaluationError("live report fields drifted")
    if (
        report["schema_version"] != LIVE_VERSION
        or report["evaluation_mode"] != "live-isolated-deny"
        or report["prompt_sha256"] != protocol.sha256_text(LITERAL_PROMPT)
        or report["publication_sha256"] != canonical.publication_sha256
    ):
        raise protocol.EvaluationError("live report protocol or source drifted")
    if report["safety"] != {
        "isolated_home": True,
        "isolated_tools_disabled_or_read_only": True,
        "isolated_approval_granted": False,
        "isolated_external_install": False,
        "captured_partial_installations": 1,
        "captured_cleanup_completed": True,
    }:
        raise protocol.EvaluationError("live report safety boundary drifted")
    attempts = report["attempts"]
    if not isinstance(attempts, list) or len(attempts) != 3:
        raise protocol.EvaluationError("live report host coverage is incomplete")
    by_host = {attempt.get("host_id"): attempt for attempt in attempts}
    if len(by_host) != len(attempts) or set(by_host) != {
        "codex",
        "claude-code",
        "agent-skills",
    }:
        raise protocol.EvaluationError("live report host coverage is forged")
    availability_values = {
        "evaluated",
        "tool-unavailable",
        "unsupported-prompt-interface",
        "authentication-unavailable",
        "response-unusable",
        "process-unavailable",
    }
    for host_id, attempt in by_host.items():
        if set(attempt) != {
            "host_id",
            "tool_available",
            "prompt_submitted",
            "availability",
            "approval_mode",
            "parsed_outcome",
            "transcript",
        }:
            raise protocol.EvaluationError("live attempt fields drifted")
        if (
            attempt["availability"] not in availability_values
            or attempt["approval_mode"] != "deny-only"
            or not isinstance(attempt["tool_available"], bool)
            or not isinstance(attempt["prompt_submitted"], bool)
        ):
            raise protocol.EvaluationError("live attempt status is invalid")
        transcript = attempt["transcript"]
        if (
            transcript["capture_kind"] != "live-isolated"
            or transcript["host_id"] != host_id
            or transcript["prompt_sha256"] != report["prompt_sha256"]
            or transcript["publication_sha256"] != report["publication_sha256"]
            or transcript["approval_decision"] != "denied"
        ):
            raise protocol.EvaluationError("live transcript binding drifted")
        outcome = protocol.parse_transcript(transcript)
        if outcome != attempt["parsed_outcome"]:
            raise protocol.EvaluationError("live parsed outcome is forged")
        derived = _attempt_from_transcript(
            host_id,
            attempt["tool_available"],
            transcript,
        )
        if derived != attempt:
            raise protocol.EvaluationError(
                "live availability or submission label contradicts transcript"
            )
    captured_failures = report["captured_failures"]
    if not isinstance(captured_failures, list) or len(captured_failures) != 1:
        raise protocol.EvaluationError(
            "captured installation failure coverage drifted"
        )
    captured = captured_failures[0]
    if set(captured) != {
        "host_id",
        "tool_available",
        "prompt_submitted",
        "availability",
        "approval_mode",
        "parsed_outcome",
        "cleanup_completed",
        "transcript",
    }:
        raise protocol.EvaluationError("captured installation failure fields drifted")
    transcript = captured["transcript"]
    if transcript["publication_sha256"] != report["publication_sha256"]:
        raise protocol.EvaluationError(
            "captured installation failure publication binding drifted"
        )
    if _captured_failure_from_transcript(transcript) != captured:
        raise protocol.EvaluationError(
            "captured installation failure labels contradict transcript"
        )
    expected_gate = _live_gate(attempts, captured_failures)
    if report["live_gate"] != expected_gate:
        raise protocol.EvaluationError("live gate contradicts live transcripts")
    if report["live_gate"]["met"] is not False:
        raise protocol.EvaluationError(
            "deny-mode unpublished evaluation cannot meet the live install gate"
        )
    protocol._validate_redaction(report)


def _live_gate(
    attempts: list[dict[str, Any]],
    captured_failures: list[dict[str, Any]],
) -> dict[str, Any]:
    required_hosts = ["agent-skills", "claude-code", "codex"]
    all_live_attempts = attempts + captured_failures
    successful_hosts = sorted(
        attempt["host_id"]
        for attempt in all_live_attempts
        if attempt["transcript"]["capture_kind"]
        in {"live-isolated", "live-captured"}
        and attempt["parsed_outcome"] == "installed-and-verified"
    )
    correctness_failures = sorted(
        attempt["host_id"]
        for attempt in captured_failures
        if attempt["parsed_outcome"] == "partial-installation"
    )
    return {
        "met": (
            successful_hosts == required_hosts
            and not correctness_failures
        ),
        "required_hosts": required_hosts,
        "successful_hosts": successful_hosts,
        "correctness_failures": correctness_failures,
    }


def _mkdir(path: Path) -> Path:
    path.mkdir(parents=True)
    return path


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    return parser.parse_args()


def main() -> int:
    if os.environ.get("KADO_INSTALL_PROMPT_LIVE") != "1":
        raise SystemExit("set KADO_INSTALL_PROMPT_LIVE=1 to run isolated live attempts")
    arguments = parse_arguments()
    report = evaluate_live_hosts()
    arguments.output.write_text(protocol.canonical_json(report), encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
