#!/usr/bin/env python3
"""Offline causal replay of the real historical controller binding."""

import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sys
import types


class FakeWorker(types.ModuleType):
    def __init__(self, measurement_root):
        super().__init__("worker")
        self.MEASUREMENT_ROOT = measurement_root
        self.REPO = measurement_root
        self.json = json
        self.luna_assignment = None
        self.deepseek_calls = 0

    def call_luna(self, *_args, **_kwargs):
        return "offline-fixture", {}

    def parse_structured_yaml(self, _text):
        return {
            "executive_result": {
                "decisions": [{
                    "question_id": "REFORMULATED-Q3-002",
                    "selected_option": "REQUEST_EVIDENCE",
                    "rationale": "offline replay fixture",
                }],
                "assignments": [copy.deepcopy(self.luna_assignment)],
                "stop": {"required": False},
            }
        }, "offline_fixture"

    def set_trajectory_question(self, _qid):
        return None

    def call_deepseek_with_tools(self, *_args, **_kwargs):
        self.deepseek_calls += 1
        return "offline-evidence", 0.0, 0, []


def load_controller(path, worker):
    sys.modules["worker"] = worker
    spec = importlib.util.spec_from_file_location("historical_loop_controller", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    module.context_block = lambda _state: ""
    module.record_candidate_questions = lambda *_args: None
    module.reconcile_questions = lambda *_args: None
    module.saturation_blockers = lambda _state: ["offline replay does not close campaign"]
    module.evidence_is_usable = lambda *_args: (True, "offline fixture")
    return module


def state_fixture():
    return {
        "current_task": {"task_id": "Q3-002-CURRENT", "question_id": "REFORMULATED-Q3-002"},
        "current_question_id": "REFORMULATED-Q3-002",
        "current_audit_result": {},
        "current_audit_result_raw": "",
        "current_evidence_raw": "",
        "current_citation_check": {"unverifiable": []},
        "_evidence_gate_blocked": None,
        "_deepseek_tool_calls_this_step": 0,
        "open_questions": [{
            "question_id": "REFORMULATED-Q3-002",
            "statement": "Inventory organizational capabilities under Q3_ONTOLOGY_V1",
            "status": "IN_PROGRESS",
        }],
        "refinement_counts": {},
        "candidate_questions": [],
        "accepted_findings": [],
        "rejected_findings": [],
        "deferred_findings": [],
        "iteration": 0,
        "mandate_run": True,
        "mission_phase": "LUNA_DISPOSITION_REQUIRED",
        "human_checkpoint_required": False,
        "stop_reason": None,
    }


def run_replay(controller, worker, legitimate, historical_drift):
    deterministic_a = controller.bind_question_refinement(copy.deepcopy(legitimate), {})
    deterministic_b = controller.bind_question_refinement(copy.deepcopy(legitimate), {})
    if deterministic_a != deterministic_b:
        raise AssertionError("same input and contract produced different gate outcomes")

    configured_sha = controller.QUESTION_IDENTITY_GATE_SHA256
    controller.QUESTION_IDENTITY_GATE_SHA256 = "0" * 64
    before_bad_identity = worker.deepseek_calls
    try:
        controller.bind_question_refinement(copy.deepcopy(legitimate), {})
    except controller.MandateHalt as exc:
        if exc.code != "instrument_regression":
            raise AssertionError(f"wrong gate identity raised {exc.code}, want instrument_regression")
    else:
        raise AssertionError("wrong gate identity did not fail closed")
    finally:
        controller.QUESTION_IDENTITY_GATE_SHA256 = configured_sha
    if worker.deepseek_calls != before_bad_identity:
        raise AssertionError("gate identity failure reached provider")

    worker.luna_assignment = historical_drift
    negative = state_fixture()
    before = worker.deepseek_calls
    negative = controller.step_luna_disposition(negative)
    after = worker.deepseek_calls
    if after != before:
        raise AssertionError(f"historical drift reached provider: before={before} after={after}")
    records = negative.get("question_target_drift_records") or []
    if len(records) != 1:
        raise AssertionError(f"historical drift record count={len(records)}, want 1")
    if records[0]["decision"]["status"] != "QUESTION_TARGET_DRIFT":
        raise AssertionError(f"unexpected drift decision: {records[0]}")

    worker.luna_assignment = legitimate
    positive = state_fixture()
    before_positive = worker.deepseek_calls
    positive = controller.step_luna_disposition(positive)
    if positive["mission_phase"] != "DEEPSEEK_EVIDENCE_REQUIRED":
        raise AssertionError(f"legitimate narrowing did not reach evidence phase: {positive}")
    if worker.deepseek_calls != before_positive:
        raise AssertionError("provider was called inside refinement acceptance")
    positive = controller.step_deepseek_evidence(positive)
    if worker.deepseek_calls != before_positive + 1:
        raise AssertionError("accepted narrowing did not call existing provider path exactly once")

    return {
        "schema_version": "INSTRUMENT_V4_CONTROLLER_REPLAY_V1",
        "historical_controller_path": str(Path(controller.__file__).resolve()),
        "historical_target_drift": {
            "status": records[0]["decision"]["status"],
            "downstream_provider_calls_before": before,
            "downstream_provider_calls_after": after,
            "drift_records": len(records),
        },
        "legitimate_narrowing": {
            "status": deterministic_a["decision"]["status"],
            "provider_calls_after_gate": worker.deepseek_calls - before_positive,
            "authorized_assignment_id": deterministic_a["authorized_task"]["assignment_id"],
        },
        "deterministic_replay": True,
        "wrong_gate_identity_fails_closed": True,
        "gate_provenance": positive["question_identity_gate_provenance"],
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--controller", required=True)
    parser.add_argument("--controller-sha256", required=True)
    parser.add_argument("--gate", required=True)
    parser.add_argument("--gate-sha256", required=True)
    parser.add_argument("--legitimate", required=True)
    parser.add_argument("--historical-drift", required=True)
    parser.add_argument("--measurement-root", required=True)
    args = parser.parse_args()

    controller_bytes = Path(args.controller).read_bytes()
    if hashlib.sha256(controller_bytes).hexdigest() != args.controller_sha256:
        raise SystemExit("controller SHA256 mismatch before replay")
    gate_bytes = Path(args.gate).read_bytes()
    if hashlib.sha256(gate_bytes).hexdigest() != args.gate_sha256:
        raise SystemExit("gate SHA256 mismatch before replay")
    os.environ["QUESTION_IDENTITY_GATE_BIN"] = str(Path(args.gate).resolve())
    os.environ["QUESTION_IDENTITY_GATE_SHA256"] = args.gate_sha256

    worker = FakeWorker(args.measurement_root)
    controller = load_controller(args.controller, worker)
    legitimate = json.loads(Path(args.legitimate).read_text(encoding="utf-8"))
    historical_drift = json.loads(Path(args.historical_drift).read_text(encoding="utf-8"))
    result = run_replay(controller, worker, legitimate, historical_drift)
    result["historical_controller_sha256"] = args.controller_sha256
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))


if __name__ == "__main__":
    main()
