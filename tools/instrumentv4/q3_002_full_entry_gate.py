#!/usr/bin/env python3
"""Offline full entry gate for the frozen REFORMULATED-Q3-002 campaign."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


SCHEMA = "Q3_002_SCIENTIFIC_CAMPAIGN_BINDING_V1"
QUESTION_ID = "REFORMULATED-Q3-002"
BASE_SHA = "588db11599d701fb1e2ecbae19aa00828663dc2b"
CONTROLLER_SHA256 = "3547b61eba43b3aad6d14eec7313fdab1b480483c7d5d55b2368aba558066c8f"
CONTROLLER_BYTES = 66401
GATE_SHA256 = "43b25561ed645231d4c74b499a99fc8c375aeb7ee947dcd86a9470e2a467f0cd"
GATE_BYTES = 3114724
SOURCE_SET_SHA256 = "5b1dd340e26a27879f30830d0b51068220b07665b0a459aaaa1f7b512d4c307a"
CONTRACT_SHA256 = "5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c"
ONTOLOGY_WHOLE_SHA256 = "0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974"
ONTOLOGY_NORMATIVE_SHA256 = "cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c"
FORBIDDEN_IDENTITIES = {"RERUN-002", "FALSIFY-Q1", "FALSIFY-Q2", "FALSIFY-Q4"}
ALLOWED_STATE_FIELDS = {
    "active_questions",
    "database_accesses",
    "model_calls",
    "model_spend_usd",
    "prior_dispositions",
    "prior_findings",
    "question_id",
    "questions",
    "refinement_counts",
    "run_id",
    "schema_version",
    "tool_call_counts",
}
REPAIR_ARTIFACTS = {
    "campaign_manifest_sha256": "docs/canonical/q3-002-campaign-binding-v1.json",
    "report_sha256": "docs/reports/q3-002-campaign-binding-repair-001.md",
    "entry_gate_sha256": "tools/instrumentv4/q3_002_campaign_entry_gate.py",
    "tests_sha256": "tools/instrumentv4/q3_002_campaign_entry_gate_test.py",
    "initial_state_sha256": "tools/instrumentv4/testdata/q3-002-clean-initial-state.json",
}


class GateFailure(RuntimeError):
    def __init__(self, code, detail):
        super().__init__(detail)
        self.code = code
        self.detail = detail


def sha256_bytes(data):
    return hashlib.sha256(data).hexdigest()


def identity(path):
    raw = Path(path).read_bytes()
    return {"sha256": sha256_bytes(raw), "bytes": len(raw), "raw": raw}


def root_probe(path):
    resolved = Path(path).resolve()

    def git(*args):
        result = subprocess.run(
            ["git", "-C", str(resolved), *args], capture_output=True,
            text=True, timeout=30, check=False,
        )
        if result.returncode:
            raise GateFailure("MEASUREMENT_ROOT_GIT_FAILURE", result.stderr.strip())
        return result.stdout.strip()

    return {
        "resolved_path": str(resolved),
        "head": git("rev-parse", "HEAD"),
        "clean": git("status", "--porcelain=v1") == "",
    }


def source_set_sha256(root):
    root = Path(root)
    paths = [root / "go.mod", root / "go.sum"]
    paths.extend(
        path
        for base in (root / "cmd/question-identity-gate", root / "internal/questionidentity")
        for path in base.rglob("*.go")
        if not path.name.endswith("_test.go")
    )
    rows = []
    for path in sorted(paths, key=lambda item: str(item.relative_to(root))):
        rows.append(
            f"{sha256_bytes(path.read_bytes())}  {path.relative_to(root)}\n".encode("utf-8")
        )
    return sha256_bytes(b"".join(rows))


def validate_static(binding, state, probe=root_probe, file_probe=identity, observed_remote_main_sha=None):
    if binding.get("schema_version") != SCHEMA:
        raise GateFailure("CAMPAIGN_SCHEMA_MISMATCH", "scientific binding schema mismatch")
    if binding.get("mission_id") != "ORGANIZATION-REDESIGN-001":
        raise GateFailure("STALE_CAMPAIGN_BINDING_DETECTED", "mission identity mismatch")
    if binding.get("question_id") != QUESTION_ID:
        raise GateFailure("STALE_CAMPAIGN_BINDING_DETECTED", "question identity mismatch")
    if binding.get("binding_role") != "SCIENTIFIC_MEASUREMENT_CANDIDATE":
        raise GateFailure("CAMPAIGN_SCOPE_MISMATCH", "binding is not scientific candidate")
    if binding.get("declared_base_sha") != BASE_SHA:
        raise GateFailure("CAMPAIGN_BASE_SHA_MISMATCH", "binding does not pin canonical candidate base")
    if binding.get("remote_main_sha_at_freeze") != BASE_SHA:
        raise GateFailure("FREEZE_CONTEXT_MISMATCH", "remote main did not equal base at freeze")
    if binding.get("initial_epistemic_state") != "UNKNOWN":
        raise GateFailure("NON_UNKNOWN_INITIAL_STATE", "campaign does not start UNKNOWN")
    if set(binding.get("active_questions") or []) != {QUESTION_ID}:
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", "binding questions are not exactly Q3-002")

    roots = binding.get("measurement_roots") or {}
    required_roots = {"measurement_root", "tool_root", "citation_root", "integrity_root"}
    if set(roots) != required_roots or len(set(roots.values())) != 1:
        raise GateFailure("ROOT_INVARIANT_FAILURE", "four roots are not one exact path")
    measurement_root = roots["measurement_root"]
    actual_root = probe(measurement_root)
    if actual_root["head"] != BASE_SHA:
        raise GateFailure("CAMPAIGN_BASE_SHA_MISMATCH", f"root HEAD {actual_root['head']} != {BASE_SHA}")
    if not actual_root["clean"]:
        raise GateFailure("MEASUREMENT_ROOT_NOT_CLEAN", "measurement root is dirty")
    if actual_root["resolved_path"] != str(Path(measurement_root).resolve()):
        raise GateFailure("ROOT_INVARIANT_FAILURE", "measurement root resolution changed")

    if set(state) != ALLOWED_STATE_FIELDS:
        extra = sorted(set(state) - ALLOWED_STATE_FIELDS)
        missing = sorted(ALLOWED_STATE_FIELDS - set(state))
        raise GateFailure("INITIAL_STATE_SCHEMA_MISMATCH", f"unexpected={extra} missing={missing}")
    serialized_state = json.dumps(state, sort_keys=True)
    stale = sorted(item for item in FORBIDDEN_IDENTITIES if item in serialized_state)
    if stale:
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", f"historical identities present: {stale}")
    if state.get("question_id") != QUESTION_ID or set(state.get("active_questions") or []) != {QUESTION_ID}:
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", "initial state is not exactly Q3-002")
    questions = state.get("questions") or []
    if len(questions) != 1 or questions[0] != {"question_id": QUESTION_ID, "status": "UNKNOWN"}:
        raise GateFailure("NON_UNKNOWN_INITIAL_STATE", "question record is not one UNKNOWN Q3-002")
    if any((state.get("model_calls") or {}).values()) or state.get("model_spend_usd") != 0.0:
        raise GateFailure("STALE_CAMPAIGN_COUNTERS_DETECTED", "model counters are not zero")
    if state.get("database_accesses") != 0 or state.get("refinement_counts") or state.get("tool_call_counts"):
        raise GateFailure("STALE_CAMPAIGN_COUNTERS_DETECTED", "DB/tool/refinement counters are not clean")
    if state.get("prior_findings") or state.get("prior_dispositions"):
        raise GateFailure("STALE_CAMPAIGN_PRIORS_DETECTED", "prior findings/dispositions are present")

    initial = binding.get("initial_state") or {}
    if initial.get("construction_method") != "CONSTRUCTED_FROM_SCHEMA_WHITELIST":
        raise GateFailure("INITIAL_STATE_PROVENANCE_FAILURE", "state is not whitelist-constructed")
    if initial.get("historical_source") is not None or initial.get("historical_state_subtraction") is not False:
        raise GateFailure("INITIAL_STATE_PROVENANCE_FAILURE", "state derives from a historical state")
    if file_probe(initial.get("artifact"))["sha256"] != initial.get("sha256"):
        raise GateFailure("INITIAL_STATE_IDENTITY_MISMATCH", "initial state artifact hash mismatch")

    controller = binding.get("controller") or {}
    if controller.get("instrument") != "INSTRUMENT_V4":
        raise GateFailure("INSTRUMENT_IDENTITY_MISMATCH", "controller is not INSTRUMENT_V4")
    if (controller.get("sha256"), controller.get("bytes")) != (CONTROLLER_SHA256, CONTROLLER_BYTES):
        raise GateFailure("CONTROLLER_IDENTITY_MISMATCH", "binding controller identity mismatch")
    actual_controller = file_probe(controller.get("path"))
    if (actual_controller["sha256"], actual_controller["bytes"]) != (CONTROLLER_SHA256, CONTROLLER_BYTES):
        raise GateFailure("CONTROLLER_IDENTITY_MISMATCH", "external controller identity mismatch")

    qid = binding.get("question_identity") or {}
    if qid.get("contract_sha256") != CONTRACT_SHA256 or qid.get("source_set_sha256") != SOURCE_SET_SHA256:
        raise GateFailure("QUESTION_IDENTITY_CONTRACT_MISMATCH", "questionidentity source/contract mismatch")
    if qid.get("binding_schema") != "INSTRUMENT_V4_CONTROLLER_BINDING_V1":
        raise GateFailure("QUESTION_IDENTITY_CONTRACT_MISMATCH", "binding schema mismatch")
    if (qid.get("gate_binary_sha256"), qid.get("gate_binary_bytes")) != (GATE_SHA256, GATE_BYTES):
        raise GateFailure("GATE_BINARY_IDENTITY_MISMATCH", "binding gate binary identity mismatch")
    actual_gate = file_probe(qid.get("gate_binary_path"))
    if (actual_gate["sha256"], actual_gate["bytes"]) != (GATE_SHA256, GATE_BYTES):
        raise GateFailure("GATE_BINARY_IDENTITY_MISMATCH", "built gate identity mismatch")
    if source_set_sha256(measurement_root) != SOURCE_SET_SHA256:
        raise GateFailure("GATE_SOURCE_IDENTITY_MISMATCH", "gate source-set mismatch")
    gate_source = Path(measurement_root, "internal/questionidentity/gate.go").read_text(encoding="utf-8")
    contract = re.search(r'CanonicalContractSHA256\s*=\s*"([0-9a-f]{64})"', gate_source)
    if not contract or contract.group(1) != CONTRACT_SHA256:
        raise GateFailure("QUESTION_IDENTITY_CONTRACT_MISMATCH", "source contract constant mismatch")

    ontology = binding.get("ontology") or {}
    if ontology.get("version") != "Q3_ONTOLOGY_V1":
        raise GateFailure("ONTOLOGY_IDENTITY_MISMATCH", "ontology version mismatch")
    raw = file_probe(Path(measurement_root, ontology.get("artifact", "")))["raw"]
    if sha256_bytes(raw) != ONTOLOGY_WHOLE_SHA256 or ontology.get("whole_file_sha256") != ONTOLOGY_WHOLE_SHA256:
        raise GateFailure("ONTOLOGY_WHOLE_FILE_MISMATCH", "ontology whole-file mismatch")
    start, end = ontology.get("normative_start_offset"), ontology.get("normative_end_offset_exclusive")
    normative = raw[start:end]
    if len(normative) != ontology.get("normative_bytes"):
        raise GateFailure("ONTOLOGY_NORMATIVE_RANGE_MISMATCH", "normative length mismatch")
    if sha256_bytes(normative) != ONTOLOGY_NORMATIVE_SHA256 or ontology.get("normative_sha256") != ONTOLOGY_NORMATIVE_SHA256:
        raise GateFailure("ONTOLOGY_NORMATIVE_HASH_MISMATCH", "normative ontology mismatch")

    repair = binding.get("repair_protection") or {}
    for key, relative in REPAIR_ARTIFACTS.items():
        if file_probe(Path(measurement_root, relative))["sha256"] != repair.get(key):
            raise GateFailure("BINDING_REPAIR_NOT_PRESENT", f"repair artifact mismatch: {relative}")

    constraints = binding.get("execution_constraints") or {}
    if not constraints or any(constraints.values()):
        raise GateFailure("CAMPAIGN_SCOPE_MISMATCH", "all pre-gate permissions must be false")

    return {
        "status": "Q3_002_STATIC_BINDING_PASS",
        "question_id": QUESTION_ID,
        "declared_base_sha": BASE_SHA,
        "measurement_root": measurement_root,
        "root_invariant": "PASS",
        "remote_main_sha_observed": observed_remote_main_sha,
        "remote_main_is_measurement_authority": False,
    }


def run_checked(command, cwd, env):
    result = subprocess.run(
        command, cwd=cwd, env=env, capture_output=True, text=True,
        timeout=180, check=False,
    )
    if result.returncode:
        raise GateFailure(
            "OFFLINE_HARNESS_FAILURE",
            f"command={command!r} rc={result.returncode} stdout={result.stdout[-1200:]} stderr={result.stderr[-1200:]}",
        )
    return result


def run_offline_harness(binding):
    root = binding["measurement_roots"]["measurement_root"]
    harness = binding["offline_harness"]
    env = os.environ.copy()
    env.update({
        "PATH": "/usr/local/go/bin:/usr/bin:/bin",
        "PYTHONDONTWRITEBYTECODE": "1",
        "REDESIGN_MEASUREMENT_ROOT": root,
        "REDESIGN_MEASUREMENT_SHA": BASE_SHA,
    })
    model_log = Path("/home/ubuntu/redesign-001/logs/model_calls.jsonl")
    ledger = Path("/home/ubuntu/redesign-001/state/budget_ledger.json")
    before = {"model_log": identity(model_log), "ledger": identity(ledger)}

    go_result = run_checked(
        ["go", "test", "./internal/questionidentity", "./cmd/question-identity-gate"], root, env
    )
    repair_result = run_checked(
        ["python3", "-m", "unittest", "-v", harness["repair_tests"]], root, env
    )
    legacy_result = run_checked(
        ["python3", harness["legacy_controller_harness"]],
        str(Path(harness["legacy_controller_harness"]).parent), env,
    )
    legacy_checks = sum(1 for line in legacy_result.stdout.splitlines() if line.startswith("PASS  "))
    if legacy_checks < harness["minimum_legacy_checks"] or "ALL PASS" not in legacy_result.stdout.splitlines():
        raise GateFailure(
            "OFFLINE_HARNESS_FAILURE",
            f"legacy harness checks={legacy_checks}, minimum={harness['minimum_legacy_checks']}",
        )

    replay_result = run_checked([
        "python3", harness["controller_replay"],
        "--controller", binding["controller"]["path"],
        "--controller-sha256", binding["controller"]["sha256"],
        "--gate", binding["question_identity"]["gate_binary_path"],
        "--gate-sha256", binding["question_identity"]["gate_binary_sha256"],
        "--legitimate", harness["typed_positive_fixture"],
        "--historical-drift", harness["historical_drift_fixture"],
        "--measurement-root", root,
    ], root, env)
    replay_lines = [line for line in replay_result.stdout.splitlines() if line.startswith("{")]
    if not replay_lines:
        raise GateFailure("OFFLINE_HARNESS_FAILURE", "controller replay emitted no JSON")
    replay = json.loads(replay_lines[-1])
    negative = replay.get("historical_target_drift") or {}
    positive = replay.get("legitimate_narrowing") or {}
    if negative.get("status") != "QUESTION_TARGET_DRIFT" or negative.get("downstream_provider_calls_after") != 0:
        raise GateFailure("OFFLINE_HARNESS_FAILURE", "historical drift replay did not block provider")
    if positive.get("status") != "ACCEPT_IDENTITY_PRESERVED" or positive.get("provider_calls_after_gate") != 1:
        raise GateFailure("OFFLINE_HARNESS_FAILURE", "typed positive replay did not reach fake provider exactly once")

    after = {"model_log": identity(model_log), "ledger": identity(ledger)}
    for key in before:
        if (before[key]["sha256"], before[key]["bytes"]) != (after[key]["sha256"], after[key]["bytes"]):
            raise GateFailure("UNEXPECTED_MODEL_OR_BUDGET_ACTIVITY", f"{key} changed during offline gate")
    if not root_probe(root)["clean"]:
        raise GateFailure("MEASUREMENT_ROOT_NOT_CLEAN", "harness dirtied measurement root")

    return {
        "go_questionidentity_tests": "PASS",
        "repair_binding_tests": "PASS",
        "legacy_controller_harness": "PASS",
        "legacy_controller_harness_checks": legacy_checks,
        "controller_replay": "PASS",
        "historical_target_drift_provider_calls": 0,
        "typed_positive_fake_provider_calls": 1,
        "model_log_unchanged": True,
        "budget_ledger_unchanged": True,
        "database_accesses": 0,
        "model_calls": 0,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binding", required=True)
    parser.add_argument("--observed-remote-main-sha")
    parser.add_argument("--run-offline-harness", action="store_true")
    args = parser.parse_args()
    try:
        binding = json.loads(Path(args.binding).read_text(encoding="utf-8"))
        state_path = binding.get("initial_state", {}).get("artifact")
        state = json.loads(Path(state_path).read_text(encoding="utf-8"))
        static = validate_static(binding, state, observed_remote_main_sha=args.observed_remote_main_sha)
        if not args.run_offline_harness:
            raise GateFailure("OFFLINE_HARNESS_REQUIRED", "full gate requires --run-offline-harness")
        harness = run_offline_harness(binding)
        outcome = {
            **static,
            "offline_harness": harness,
            "status": "Q3_002_FULL_ENTRY_GATE_PASS",
            "measurement_calls_permitted_after_gate": True,
            "q3_002_executed": False,
            "production_mutated": False,
        }
        print(json.dumps(outcome, sort_keys=True, separators=(",", ":")))
        return 0
    except (GateFailure, OSError, ValueError, TypeError, json.JSONDecodeError) as exc:
        code = exc.code if isinstance(exc, GateFailure) else "FULL_ENTRY_GATE_OPERATIONAL_FAILURE"
        detail = exc.detail if isinstance(exc, GateFailure) else str(exc)
        print(json.dumps({
            "status": code,
            "detail": detail,
            "model_calls": 0,
            "database_accesses": 0,
            "q3_002_executed": False,
        }, sort_keys=True))
        return 1


if __name__ == "__main__":
    sys.exit(main())
