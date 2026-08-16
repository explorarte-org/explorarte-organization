#!/usr/bin/env python3
"""Run the first model-assisted Q3-002 classification after sealed registration."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import yaml


BASE = "588db11599d701fb1e2ecbae19aa00828663dc2b"
ROOT = Path("/home/ubuntu/campaign-588db11")
MEAS = Path("/home/ubuntu/q3-002-measurement-001")
REPORT = MEAS / "docs/reports/q3-002-measurement-001"
RUN = REPORT / "model-run"
WORKER_PATH = Path("/home/ubuntu/redesign-001/worker.py")
EXPECTED = {
    ROOT / "docs/canonical/q3-organizational-capability-ontology-v1.md": "0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974",
    Path("/home/ubuntu/redesign-001/loop_controller.py"): "3547b61eba43b3aad6d14eec7313fdab1b480483c7d5d55b2368aba558066c8f",
    Path("/home/ubuntu/q3-002-full-entry-gate-001/bin/question-identity-gate"): "43b25561ed645231d4c74b499a99fc8c375aeb7ee947dcd86a9470e2a467f0cd",
    REPORT / "source-registration/source-universe.jsonl": "a4a7bacbefb1d8f040f582e046a1ccdc0d2088b2c3ee2a42df65ec9d2ddfca43",
    REPORT / "source-registration/go-symbol-universe.jsonl": "2d5a15df73f8b62a8fd67ad53dcc7ce45c91fce5cb051cd24e339ba53ec58b1f",
    REPORT / "runtime-registration/runtime-universe.jsonl": "b22d3801be5d8989d1a0025a2bedab7c5cb7656e12f912f12e2ffbc631f406e7",
    REPORT / "runtime-registration/runtime-observations.jsonl": "a73dfe6cbe73c7ff5da52cae44051cc7cb73b3e726ed67508e9c2aacc5b7b78e",
    REPORT / "compact-candidate-index.md": "c1401ee48427c2f1725de95bb7dd14c00af84e51478fc864519bd814444a3fab",
}


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def preflight() -> None:
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
    status = subprocess.run(["git", "status", "--porcelain=v1"], cwd=ROOT, check=True, capture_output=True, text=True).stdout
    if head != BASE or status:
        raise SystemExit("STOP: scientific root SHA/clean invariant failed")
    for path, expected in EXPECTED.items():
        actual = digest(path)
        if actual != expected:
            raise SystemExit(f"STOP: identity drift {path}: {actual} != {expected}")
    if RUN.exists():
        raise SystemExit("STOP: model-run already exists; refusing state reuse")


def load_worker():
    os.environ["REDESIGN_MEASUREMENT_ROOT"] = str(ROOT)
    spec = importlib.util.spec_from_file_location("q3_002_worker", WORKER_PATH)
    worker = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(worker)
    return worker


def main() -> None:
    preflight()
    RUN.mkdir(parents=True, exist_ok=False)
    (RUN / "reasoning_traces").mkdir()
    worker = load_worker()
    worker.LEDGER_PATH = str(RUN / "budget-ledger.json")
    worker.CALLS_LOG = str(RUN / "model-calls.jsonl")
    worker.TRANSPORT_LOG = str(RUN / "transport-attempts.jsonl")
    worker.TOOL_TRAJECTORY = str(RUN / "tool-trajectory.jsonl")
    worker.REASONING_TRACE_DIR = str(RUN / "reasoning_traces")
    worker.BUDGET = {
        "deepseek": {"target": 0.25, "hard": 0.40},
        "grok": {"target": 0.65, "hard": 0.85},
        "luna": {"target": 0.05, "hard": 0.10},
        "mission": {"target": 0.80, "hard": 1.25},
    }
    worker.save_ledger({"deepseek": 0.0, "grok": 0.0, "luna": 0.0, "mission": 0.0})
    worker.set_trajectory_question("REFORMULATED-Q3-002")
    for tool in worker.TOOLS_SCHEMA:
        desc = tool["function"].get("description", "")
        tool["function"]["description"] = desc.replace("main@4e0853d3", f"detached@{BASE}")

    ontology = (ROOT / "docs/canonical/q3-organizational-capability-ontology-v1.md").read_bytes()[415:11987].decode("utf-8")
    compact = (REPORT / "compact-candidate-index.md").read_text()
    runtime_categories = (REPORT / "runtime-registration/runtime-categorical-evidence.jsonl").read_text()
    prompt = f"""
STRICT MEASUREMENT ASSIGNMENT

question_id: REFORMULATED-Q3-002
initial_epistemic_state: UNKNOWN
measurement_root: {ROOT}
measurement_sha: {BASE}
ontology_version: Q3_ONTOLOGY_V1
ontology_whole_file_sha256: 0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974
ontology_normative_sha256: cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c
question_identity_contract_sha256: 5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c

SUBJECT: organizational_capabilities_implemented_by_this_repository
REQUESTED_RELATION: declared_or_configured_capability <-> surviving_runtime_evidence
MEASUREMENT_UNIVERSE: deterministically_registered_accessible_source_space + surviving_runtime_evidence_universe

The source universe (1,060 tracked artifacts) and runtime universe (103 accessible
tables/views) were sealed externally before this call. You do NOT decide whether
enumeration is complete. You assist classification and relation-building only.
There is no expected capability count. Never optimize toward a number and never
replace the task with a literal phrase/count question.

Apply Q1-Q6 in order to every proposed positive capability. A positive candidate
without every required record field and a verifiable behavioral_contract is
invalid and must be UNRESOLVED. Package, file, table, migration, role, event type,
endpoint, struct, interface, fixture, helper, or config row is never a capability
by artifact identity. Merge/split by responsibility, causal purpose, and compatible
entry/transition contract only.

Use read_file/grep_repo/list_dir against the exact frozen root to inspect actual
implementation. Do not query the database: the sealed runtime observations below
are the only runtime packet for this call. Cite repository paths with exact line
or symbol locators that you actually inspected. Treat zero observations as no
surviving evidence, never as historical absence.

Return ONLY valid YAML with exactly this top-level shape:

evidence_packet:
  question_id: REFORMULATED-Q3-002
  conclusion: <provisional inventory statement, not a bare count>
  observations: [<non-empty OBSERVED/DERIVED items>]
  provenance: [<non-empty path:line-or-symbol items>]
  counterevidence: []
  unknowns: []
  coverage_complete: false
  cheapest_next_evidence: <specific>
  capability_candidates:
    - capability_id: OC-...
      name: ...
      behavioral_contract: ...
      causal_purpose: ...
      entrypoints_or_triggers: []
      enforcement_or_transition_points: []
      reads_or_inputs: []
      writes_or_effects: []
      implementation_provenance: []
      declaration_provenance: []
      configuration_provenance: []
      possible_runtime_evidence: []
      boundary_with_adjacent_capabilities: ...
      classification_basis: "Q1=...; Q2=...; Q3=...; Q4=...; Q5=...; Q6=..."
  supporting_or_negative_candidates: []
  navigation_group_accounting:
    - group: internal/example
      classification: RELEVANT_CAPABILITY_EVIDENCE|IRRELEVANT_WITH_PROOF|UNRESOLVED
      capability_ids: []
      reason: ...
      provenance: []
  runtime_relations:
    - runtime_unit: public.example
      capability_ids: []
      relation: SURVIVING_EVIDENCE|DECLARATION_OR_CONFIGURATION_ONLY|IRRELEVANT_WITH_PROOF|UNRESOLVED
      reason: ...

The 70 navigation groups must each appear exactly once in navigation_group_accounting.
All 103 runtime units must each appear exactly once in runtime_relations, including
empty tables. Honest UNRESOLVED is required when evidence is insufficient. Set
coverage_complete=false because completeness is reserved to the external checker.

FROZEN NORMATIVE ONTOLOGY REGION
---
{ontology}
---

DETERMINISTIC COMPACT NAVIGATION INDEX
---
{compact}
---

SEALED RUNTIME CATEGORICAL OBSERVATIONS (JSONL)
---
{runtime_categories}
---
"""
    (RUN / "deepseek-prompt.txt").write_text(prompt)
    text, cost, tool_calls, messages = worker.call_deepseek_with_tools(
        prompt, task_id="REFORMULATED-Q3-002-EVIDENCE-001", max_iters=18,
    )
    (RUN / "deepseek-raw.yaml").write_text(text)
    parsed = yaml.safe_load(text)
    packet = parsed.get("evidence_packet") if isinstance(parsed, dict) else None
    if not isinstance(packet, dict):
        raise SystemExit("STOP_EVIDENCE_GATE: missing evidence_packet")
    if packet.get("question_id") != "REFORMULATED-Q3-002":
        raise SystemExit("STOP_EVIDENCE_GATE: wrong question_id")
    if not packet.get("observations") or not packet.get("provenance"):
        raise SystemExit("STOP_EVIDENCE_GATE: observations/provenance empty")
    if packet.get("coverage_complete") is not False:
        raise SystemExit("STOP_COMPLETENESS_SELF_ATTESTATION")
    if not isinstance(packet.get("capability_candidates"), list):
        raise SystemExit("STOP_EVIDENCE_GATE: capability_candidates missing")
    result = {
        "question_id": "REFORMULATED-Q3-002",
        "deepseek_cost": cost,
        "deepseek_tool_calls": tool_calls,
        "capability_candidate_count_is_derived_not_expected": len(packet["capability_candidates"]),
        "navigation_group_records": len(packet.get("navigation_group_accounting") or []),
        "runtime_relation_records": len(packet.get("runtime_relations") or []),
        "coverage_complete_claim": packet.get("coverage_complete"),
        "raw_sha256": digest(RUN / "deepseek-raw.yaml"),
    }
    (RUN / "deepseek-result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
