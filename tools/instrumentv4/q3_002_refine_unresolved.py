#!/usr/bin/env python3
"""Gate and execute the typed Q3-002 unresolved-source refinement."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import yaml


BASE = "588db11599d701fb1e2ecbae19aa00828663dc2b"
ROOT = Path("/home/ubuntu/campaign-588db11")
MEAS = Path("/home/ubuntu/q3-002-measurement-001")
REPORT = MEAS / "docs/reports/q3-002-measurement-001"
RUN = REPORT / "model-run"
GATE = Path("/home/ubuntu/q3-002-full-entry-gate-001/bin/question-identity-gate")
GATE_SHA = "43b25561ed645231d4c74b499a99fc8c375aeb7ee947dcd86a9470e2a467f0cd"
REQUEST = MEAS / "docs/reports/q3-002-measurement-001/q3-002-source-refinement-request.json"
WORKER_PATH = Path("/home/ubuntu/redesign-001/worker.py")
REQUIRED_CAPABILITY_FIELDS = {
    "capability_id", "name", "behavioral_contract", "causal_purpose",
    "entrypoints_or_triggers", "enforcement_or_transition_points",
    "reads_or_inputs", "writes_or_effects", "implementation_provenance",
    "declaration_provenance", "configuration_provenance",
    "possible_runtime_evidence", "boundary_with_adjacent_capabilities",
    "classification_basis",
}


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_worker():
    os.environ["REDESIGN_MEASUREMENT_ROOT"] = str(ROOT)
    spec = importlib.util.spec_from_file_location("q3_002_worker_refine", WORKER_PATH)
    worker = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(worker)
    return worker


def main() -> None:
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
    status = subprocess.run(["git", "status", "--porcelain=v1"], cwd=ROOT, check=True, capture_output=True, text=True).stdout
    if head != BASE or status or digest(GATE) != GATE_SHA:
        raise SystemExit("STOP: root or gate identity drift")
    if (RUN / "deepseek-refinement-raw.txt").exists():
        raise SystemExit("STOP: refinement state already exists")

    request_bytes = REQUEST.read_bytes()
    gated = subprocess.run([str(GATE)], input=request_bytes, check=True, capture_output=True)
    outcome = json.loads(gated.stdout)
    (RUN / "source-refinement-gate-outcome.json").write_bytes(gated.stdout)
    decision = outcome.get("decision") or {}
    if decision.get("status") != "ACCEPT_IDENTITY_PRESERVED" or not outcome.get("provider_call_allowed"):
        raise SystemExit("QUESTION_TARGET_DRIFT: refinement rejected; provider_calls=0")
    task = outcome.get("authorized_task") or {}
    if task.get("question_id") != "REFORMULATED-Q3-002":
        raise SystemExit("STOP: authorized task question mismatch")

    initial = yaml.safe_load((RUN / "deepseek-packet.yaml").read_text())["evidence_packet"]
    unresolved = sorted(
        row["group"] for row in initial["navigation_group_accounting"]
        if row["classification"] == "UNRESOLVED"
    )
    if not unresolved:
        raise SystemExit("STOP: no unresolved groups to narrow")
    index = (REPORT / "compact-candidate-index.md").read_text()
    sections = {}
    for match in re.finditer(r"^## (.+?) — artifacts=.*?(?=^## |^# Runtime)", index, flags=re.MULTILINE | re.DOTALL):
        sections[match.group(1)] = match.group(0)
    if set(unresolved) - set(sections):
        raise SystemExit("STOP: unresolved group absent from registered index")
    narrow_index = "\n\n".join(sections[group] for group in unresolved)
    existing = [{
        "capability_id": cap["capability_id"],
        "name": cap["name"],
        "behavioral_contract": cap["behavioral_contract"],
        "boundary_with_adjacent_capabilities": cap["boundary_with_adjacent_capabilities"],
    } for cap in initial["capability_candidates"]]
    ontology = (ROOT / "docs/canonical/q3-organizational-capability-ontology-v1.md").read_bytes()[415:11987].decode()

    prompt = f"""
AUTHORIZED TYPED REFINEMENT

question_id: REFORMULATED-Q3-002
profile: resolve_unclassified_source_entries_against_proposed_capabilities
gate_status: ACCEPT_IDENTITY_PRESERVED
authorized_assignment_id: {task.get('assignment_id')}

Inspect ONLY the 23 already registered unresolved navigation groups below. For
each group: relate its source entries to an already proposed capability, prove
the group irrelevant-with-proof, propose one or more genuinely new capabilities
with complete Q1-Q6 records, or leave it unresolved. Package identity is never
capability identity. Do not alter the subject, relation, universes, output schema,
or runtime accounting. No literal mechanism phrase/count. No expected count.
Do not use sql_select. Use repository tools against {ROOT}@{BASE}; cite only files
and symbols actually read. A new positive record lacking a verifiable behavioral
contract or evidenced adjacent boundary is invalid.

Return ONLY YAML with this shape:
refinement_packet:
  question_id: REFORMULATED-Q3-002
  profile: resolve_unclassified_source_entries_against_proposed_capabilities
  observations: []
  provenance: []
  new_capability_candidates: []
  supporting_relations:
    - group: ...
      capability_ids: []
      reason: ...
      provenance: []
  resolved_group_accounting:
    - group: ...
      classification: RELEVANT_CAPABILITY_EVIDENCE|IRRELEVANT_WITH_PROOF|UNRESOLVED
      capability_ids: []
      reason: ...
      provenance: []
  remaining_unknowns: []
  coverage_complete: false

Every one of the 23 groups must appear exactly once in resolved_group_accounting.
The external checker, not you, determines overall completeness.

EXISTING PROPOSED CAPABILITIES
---
{yaml.safe_dump(existing, sort_keys=False)}
---

FROZEN NORMATIVE ONTOLOGY
---
{ontology}
---

REGISTERED UNRESOLVED NAVIGATION GROUPS
---
{narrow_index}
---
"""
    (RUN / "deepseek-refinement-prompt.txt").write_text(prompt)
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
    worker.set_trajectory_question("REFORMULATED-Q3-002")
    text, cost, tool_calls, _ = worker.call_deepseek_with_tools(
        prompt, task_id="Q3-002-RESOLVE-UNCLASSIFIED-SOURCE-ENTRIES", max_iters=14,
    )
    (RUN / "deepseek-refinement-raw.txt").write_text(text)
    blocks = re.findall(r"```yaml\s*\n(.*?)\n```", text, flags=re.DOTALL)
    if len(blocks) == 1:
        payload = blocks[0]
        normalization = "sole YAML fence extracted verbatim"
    elif not blocks:
        payload = text
        normalization = "raw response already YAML"
    else:
        raise SystemExit("STOP_EVIDENCE_GATE: multiple YAML fences")
    parsed = yaml.safe_load(payload)
    packet = parsed.get("refinement_packet") if isinstance(parsed, dict) else None
    if not isinstance(packet, dict) or packet.get("question_id") != "REFORMULATED-Q3-002":
        raise SystemExit("STOP_EVIDENCE_GATE: invalid refinement packet")
    if packet.get("profile") != "resolve_unclassified_source_entries_against_proposed_capabilities":
        raise SystemExit("STOP_EVIDENCE_GATE: refinement profile drift")
    if packet.get("coverage_complete") is not False:
        raise SystemExit("STOP_COMPLETENESS_SELF_ATTESTATION")
    rows = packet.get("resolved_group_accounting") or []
    actual = [row.get("group") for row in rows if isinstance(row, dict)]
    if len(actual) != len(set(actual)) or set(actual) != set(unresolved):
        raise SystemExit("STOP_EVIDENCE_GATE: unresolved group accounting mismatch")
    errors = []
    for cap in packet.get("new_capability_candidates") or []:
        missing = sorted(REQUIRED_CAPABILITY_FIELDS - set(cap))
        if missing or not cap.get("behavioral_contract"):
            errors.append({"capability_id": cap.get("capability_id"), "missing": missing})
    if errors:
        raise SystemExit(f"STOP_EVIDENCE_GATE: invalid positive records {errors}")
    packet_path = RUN / "deepseek-refinement-packet.yaml"
    packet_path.write_text(payload)
    result = {
        "gate_status": decision["status"],
        "provider_call_allowed": outcome["provider_call_allowed"],
        "normalization": normalization,
        "unresolved_group_count_before": len(unresolved),
        "group_records": len(rows),
        "new_capability_candidates": len(packet.get("new_capability_candidates") or []),
        "remaining_unresolved_groups": sum(1 for row in rows if row.get("classification") == "UNRESOLVED"),
        "tool_calls": tool_calls,
        "deepseek_call_cost": cost,
        "packet_sha256": digest(packet_path),
        "ledger": worker.load_ledger(),
    }
    (RUN / "deepseek-refinement-result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
