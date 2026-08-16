#!/usr/bin/env python3
"""Fail-closed offline campaign binding gate for REFORMULATED-Q3-002."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


SCHEMA = "Q3_002_CAMPAIGN_BINDING_V1"
QUESTION_ID = "REFORMULATED-Q3-002"
INSTRUMENT = "INSTRUMENT_V4"
MISSION_ID = "ORGANIZATION-REDESIGN-001"
PHASE_ID = "Q3_002_CAMPAIGN_BINDING_REPAIR_001"
DECLARED_BASE_SHA = "37ed48d97cfda9efaf4f660a4f626745d793107e"
CONTROLLER_SHA256 = "3547b61eba43b3aad6d14eec7313fdab1b480483c7d5d55b2368aba558066c8f"
CONTROLLER_BYTES = 66401
GATE_BINARY_SHA256 = "43b25561ed645231d4c74b499a99fc8c375aeb7ee947dcd86a9470e2a467f0cd"
GATE_BINARY_BYTES = 3114724
SOURCE_SET_SHA256 = "5b1dd340e26a27879f30830d0b51068220b07665b0a459aaaa1f7b512d4c307a"
QUESTION_IDENTITY_CONTRACT_SHA256 = "5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c"
ONTOLOGY_WHOLE_FILE_SHA256 = "0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974"
ONTOLOGY_NORMATIVE_SHA256 = "cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c"
FORBIDDEN_QUESTIONS = {"FALSIFY-Q1", "FALSIFY-Q2", "FALSIFY-Q4"}
ALLOWED_INITIAL_STATE_FIELDS = {
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


class GateFailure(RuntimeError):
    def __init__(self, code, detail):
        super().__init__(detail)
        self.code = code
        self.detail = detail


def sha256_bytes(data):
    return hashlib.sha256(data).hexdigest()


def file_identity(path):
    raw = Path(path).read_bytes()
    return {"sha256": sha256_bytes(raw), "bytes": len(raw), "raw": raw}


def git_root_probe(path):
    root = Path(path).resolve()

    def git(*args):
        proc = subprocess.run(
            ["git", "-C", str(root), *args],
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
        if proc.returncode != 0:
            raise GateFailure("MEASUREMENT_ROOT_GIT_FAILURE", proc.stderr.strip())
        return proc.stdout.strip()

    return {
        "resolved_path": str(root),
        "head": git("rev-parse", "HEAD"),
        "clean": git("status", "--porcelain=v1") == "",
    }


def source_set_sha256(root):
    root = Path(root)
    paths = [root / "go.mod", root / "go.sum"]
    paths.extend(
        p for base in (root / "cmd/question-identity-gate", root / "internal/questionidentity")
        for p in base.rglob("*.go") if not p.name.endswith("_test.go")
    )
    lines = []
    for path in sorted(paths, key=lambda p: str(p.relative_to(root))):
        relative = path.relative_to(root)
        lines.append(f"{sha256_bytes(path.read_bytes())}  {relative}\n".encode("utf-8"))
    return sha256_bytes(b"".join(lines))


def stale_state_reason(state):
    serialized = json.dumps(state, sort_keys=True)
    if "RERUN-002" in serialized:
        return "state contains RERUN-002 identity"
    present = sorted(qid for qid in FORBIDDEN_QUESTIONS if qid in serialized)
    if present:
        return f"state contains historical questions: {present}"
    return ""


def validate_binding(campaign, state, root_probe, artifact_probe, observed_remote_main_sha=None):
    if campaign.get("schema_version") != SCHEMA:
        raise GateFailure("CAMPAIGN_SCHEMA_MISMATCH", "unsupported campaign schema")
    if campaign.get("mission_id") != MISSION_ID or campaign.get("phase_id") != PHASE_ID:
        raise GateFailure("STALE_CAMPAIGN_BINDING_DETECTED", "mission/phase identity mismatch")
    if campaign.get("binding_role") != "REPAIR_DEVELOPMENT_AND_VALIDATION_ONLY":
        raise GateFailure("CAMPAIGN_SCOPE_MISMATCH", "repair binding cannot self-authorize measurement")
    if campaign.get("question_id") != QUESTION_ID:
        raise GateFailure("STALE_CAMPAIGN_BINDING_DETECTED", "campaign question_id is not Q3-002")
    if set(campaign.get("active_questions") or []) != {QUESTION_ID}:
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", "campaign active_questions is not exactly Q3-002")
    if stale_state_reason(state):
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", stale_state_reason(state))
    initial_state = campaign.get("initial_state") or {}
    if initial_state.get("construction_method") != "CONSTRUCTED_FROM_SCHEMA_WHITELIST":
        raise GateFailure("INITIAL_STATE_PROVENANCE_FAILURE", "initial state is not schema-constructed")
    if initial_state.get("historical_source") is not None or initial_state.get("historical_state_subtraction") is not False:
        raise GateFailure("INITIAL_STATE_PROVENANCE_FAILURE", "initial state derives from historical state")
    if set(initial_state.get("allowed_fields") or []) != ALLOWED_INITIAL_STATE_FIELDS:
        raise GateFailure("INITIAL_STATE_SCHEMA_MISMATCH", "campaign initial-state whitelist mismatch")
    if set(state) != ALLOWED_INITIAL_STATE_FIELDS:
        extra = sorted(set(state) - ALLOWED_INITIAL_STATE_FIELDS)
        missing = sorted(ALLOWED_INITIAL_STATE_FIELDS - set(state))
        raise GateFailure("INITIAL_STATE_SCHEMA_MISMATCH", f"unexpected={extra} missing={missing}")
    state_identity = artifact_probe(initial_state.get("artifact"))
    if state_identity["sha256"] != initial_state.get("sha256"):
        raise GateFailure("INITIAL_STATE_IDENTITY_MISMATCH", "clean initial-state artifact hash mismatch")
    if state.get("schema_version") != "Q3_002_INITIAL_STATE_V1":
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", "unsupported initial-state schema")
    if state.get("run_id") != "Q3-002-CANONICAL-MEASUREMENT" or state.get("question_id") != QUESTION_ID:
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", "state question_id is not Q3-002")
    if set(state.get("active_questions") or []) != {QUESTION_ID}:
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", "state active_questions is not exactly Q3-002")
    questions = state.get("questions") or []
    question_ids = {item.get("question_id") for item in questions}
    if len(questions) != 1 or question_ids != {QUESTION_ID}:
        raise GateFailure("STALE_CAMPAIGN_STATE_DETECTED", "state questions is not exactly Q3-002")
    if any(item.get("status") != "UNKNOWN" for item in state.get("questions") or []):
        raise GateFailure("NON_UNKNOWN_INITIAL_STATE", "Q3-002 must start at UNKNOWN")
    if any((state.get("model_calls") or {}).values()) or state.get("model_spend_usd") != 0.0:
        raise GateFailure("STALE_CAMPAIGN_COUNTERS_DETECTED", "model counters/spend are not zero")
    if state.get("database_accesses") != 0:
        raise GateFailure("STALE_CAMPAIGN_COUNTERS_DETECTED", "database access counter is not zero")
    if state.get("refinement_counts") or state.get("tool_call_counts"):
        raise GateFailure("STALE_CAMPAIGN_COUNTERS_DETECTED", "tool/refinement counters are not empty")
    if state.get("prior_findings") or state.get("prior_dispositions"):
        raise GateFailure("STALE_CAMPAIGN_PRIORS_DETECTED", "prior findings/dispositions are not empty")

    if campaign.get("initial_epistemic_state") != "UNKNOWN":
        raise GateFailure("NON_UNKNOWN_INITIAL_STATE", "campaign must declare UNKNOWN")
    constraints = campaign.get("execution_constraints") or {}
    required_constraints = {
        "rerun_002_state_reuse_allowed",
        "rerun_002_budget_reuse_allowed",
        "historical_questions_allowed",
        "model_calls_during_repair_allowed",
        "database_access_during_repair_allowed",
        "q3_002_execution_during_repair_allowed",
        "final_scientific_base_frozen_before_repair_merge_allowed",
    }
    if set(constraints) != required_constraints or any(constraints.values()):
        raise GateFailure("CAMPAIGN_SCOPE_MISMATCH", "repair constraints must all be present and false")

    declared = campaign.get("declared_base_sha")
    if not re.fullmatch(r"[0-9a-f]{40}", declared or ""):
        raise GateFailure("CAMPAIGN_BASE_SHA_INVALID", "declared_base_sha is not a full SHA")
    if declared != DECLARED_BASE_SHA:
        raise GateFailure("CAMPAIGN_BASE_SHA_MISMATCH", "campaign did not load the frozen Q3-002 base")
    if campaign.get("remote_main_sha_at_freeze") != declared:
        raise GateFailure("FREEZE_CONTEXT_MISMATCH", "remote main at freeze did not equal declared base")

    roots = campaign.get("measurement_roots") or {}
    required_roots = {"measurement_root", "tool_root", "citation_root", "integrity_root"}
    if set(roots) != required_roots or len(set(roots.values())) != 1:
        raise GateFailure("ROOT_INVARIANT_FAILURE", "four configured roots are not one exact path")
    configured_root = roots["measurement_root"]
    root = root_probe(configured_root)
    if root["resolved_path"] != str(Path(configured_root).resolve()):
        raise GateFailure("ROOT_INVARIANT_FAILURE", "measurement root did not resolve byte-for-byte")
    if root["head"] != declared:
        raise GateFailure(
            "CAMPAIGN_BASE_SHA_MISMATCH",
            f"measurement root HEAD {root['head']} != declared base {declared}",
        )
    if not root["clean"]:
        raise GateFailure("MEASUREMENT_ROOT_NOT_CLEAN", "measurement root has modified/untracked material")

    controller = campaign.get("controller") or {}
    if controller.get("instrument") != INSTRUMENT:
        raise GateFailure("INSTRUMENT_IDENTITY_MISMATCH", "controller is not INSTRUMENT_V4")
    if (controller.get("sha256"), controller.get("bytes")) != (CONTROLLER_SHA256, CONTROLLER_BYTES):
        raise GateFailure("CONTROLLER_IDENTITY_MISMATCH", "campaign controller identity is not frozen V4")
    controller_actual = artifact_probe(controller.get("path"))
    if (controller_actual["sha256"], controller_actual["bytes"]) != (
        controller.get("sha256"), controller.get("bytes")
    ):
        raise GateFailure("CONTROLLER_IDENTITY_MISMATCH", "bound controller hash/size mismatch")

    identity = campaign.get("question_identity") or {}
    if identity.get("binding_schema") != "INSTRUMENT_V4_CONTROLLER_BINDING_V1":
        raise GateFailure("QUESTION_IDENTITY_CONTRACT_MISMATCH", "binding schema mismatch")
    if identity.get("contract_sha256") != QUESTION_IDENTITY_CONTRACT_SHA256:
        raise GateFailure("QUESTION_IDENTITY_CONTRACT_MISMATCH", "campaign contract identity mismatch")
    if identity.get("source_set_sha256") != SOURCE_SET_SHA256:
        raise GateFailure("GATE_SOURCE_IDENTITY_MISMATCH", "campaign gate source identity mismatch")
    if (identity.get("gate_binary_sha256"), identity.get("gate_binary_bytes")) != (
        GATE_BINARY_SHA256, GATE_BINARY_BYTES
    ):
        raise GateFailure("GATE_BINARY_IDENTITY_MISMATCH", "campaign gate binary identity mismatch")
    gate_actual = artifact_probe(identity.get("gate_binary_path"))
    if (gate_actual["sha256"], gate_actual["bytes"]) != (
        identity.get("gate_binary_sha256"), identity.get("gate_binary_bytes")
    ):
        raise GateFailure("GATE_BINARY_IDENTITY_MISMATCH", "questionidentity binary hash/size mismatch")

    actual_source_set = source_set_sha256(configured_root)
    if actual_source_set != identity.get("source_set_sha256"):
        raise GateFailure("GATE_SOURCE_IDENTITY_MISMATCH", "questionidentity source-set hash mismatch")
    gate_source = Path(configured_root, "internal/questionidentity/gate.go").read_text(encoding="utf-8")
    match = re.search(r'CanonicalContractSHA256\s*=\s*"([0-9a-f]{64})"', gate_source)
    if not match or match.group(1) != identity.get("contract_sha256"):
        raise GateFailure("QUESTION_IDENTITY_CONTRACT_MISMATCH", "frozen contract hash mismatch")

    ontology = campaign.get("ontology") or {}
    if ontology.get("version") != "Q3_ONTOLOGY_V1":
        raise GateFailure("ONTOLOGY_IDENTITY_MISMATCH", "ontology version mismatch")
    if ontology.get("whole_file_sha256") != ONTOLOGY_WHOLE_FILE_SHA256:
        raise GateFailure("ONTOLOGY_WHOLE_FILE_MISMATCH", "campaign whole-file ontology identity mismatch")
    if ontology.get("normative_sha256") != ONTOLOGY_NORMATIVE_SHA256:
        raise GateFailure("ONTOLOGY_NORMATIVE_HASH_MISMATCH", "campaign normative ontology identity mismatch")
    ontology_path = Path(configured_root, ontology.get("artifact", ""))
    ontology_actual = artifact_probe(ontology_path)
    if ontology_actual["sha256"] != ontology.get("whole_file_sha256"):
        raise GateFailure("ONTOLOGY_WHOLE_FILE_MISMATCH", "ontology whole-file hash mismatch")
    start = ontology.get("normative_start_offset")
    end = ontology.get("normative_end_offset_exclusive")
    normative = ontology_actual["raw"][start:end]
    if len(normative) != ontology.get("normative_bytes"):
        raise GateFailure("ONTOLOGY_NORMATIVE_RANGE_MISMATCH", "ontology normative byte length mismatch")
    if sha256_bytes(normative) != ontology.get("normative_sha256"):
        raise GateFailure("ONTOLOGY_NORMATIVE_HASH_MISMATCH", "ontology normative hash mismatch")

    return {
        "status": "Q3_002_CAMPAIGN_BINDING_VALID",
        "question_id": QUESTION_ID,
        "declared_base_sha": declared,
        "measurement_root": configured_root,
        "root_invariant": "PASS",
        "measurement_root_clean": True,
        "active_questions": [QUESTION_ID],
        "controller": INSTRUMENT,
        "ontology_version": ontology.get("version"),
        "ontology_whole_file_sha256": ontology.get("whole_file_sha256"),
        "ontology_normative_sha256": ontology.get("normative_sha256"),
        "question_identity_contract_sha256": identity.get("contract_sha256"),
        "remote_main_sha_observed": observed_remote_main_sha,
        "remote_main_is_runtime_authority": False,
        "model_calls": 0,
        "database_accesses": 0,
        "q3_002_executed": False,
        "binding_role": "REPAIR_DEVELOPMENT_AND_VALIDATION_ONLY",
        "final_scientific_base_status": "TO_BE_FROZEN_AFTER_REPAIR_MERGE",
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--campaign", required=True)
    parser.add_argument("--state", required=True)
    parser.add_argument("--observed-remote-main-sha")
    args = parser.parse_args()
    campaign = json.loads(Path(args.campaign).read_text(encoding="utf-8"))
    state = json.loads(Path(args.state).read_text(encoding="utf-8"))
    try:
        outcome = validate_binding(
            campaign,
            state,
            root_probe=git_root_probe,
            artifact_probe=file_identity,
            observed_remote_main_sha=args.observed_remote_main_sha,
        )
    except (GateFailure, OSError, ValueError, TypeError) as exc:
        code = exc.code if isinstance(exc, GateFailure) else "CAMPAIGN_BINDING_OPERATIONAL_FAILURE"
        detail = exc.detail if isinstance(exc, GateFailure) else str(exc)
        print(json.dumps({"status": code, "detail": detail, "model_calls": 0, "database_accesses": 0}, sort_keys=True))
        return 1
    print(json.dumps(outcome, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    sys.exit(main())
