#!/usr/bin/env python3
"""Audit the valid partial Q3-002 DeepSeek packet with Grok, no tools."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import yaml


BASE = "588db11599d701fb1e2ecbae19aa00828663dc2b"
ROOT = Path("/home/ubuntu/campaign-588db11")
MEAS = Path("/home/ubuntu/q3-002-measurement-001")
REPORT = MEAS / "docs/reports/q3-002-measurement-001"
RUN = REPORT / "model-run"
WORKER_PATH = Path("/home/ubuntu/redesign-001/worker.py")


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def preflight() -> dict:
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
    status = subprocess.run(["git", "status", "--porcelain=v1"], cwd=ROOT, check=True, capture_output=True, text=True).stdout
    if head != BASE or status:
        raise SystemExit("STOP: scientific root SHA/clean invariant failed")
    validation = json.loads((RUN / "deepseek-validation.json").read_text())
    if not validation.get("structural_evidence_gate_pass"):
        raise SystemExit("STOP_EVIDENCE_GATE: DeepSeek packet invalid")
    if validation.get("coverage_complete_claim") is not False:
        raise SystemExit("STOP_COMPLETENESS_SELF_ATTESTATION")
    if (RUN / "grok-raw.txt").exists():
        raise SystemExit("STOP: Grok output already exists; refusing state reuse")
    return validation


def load_worker():
    os.environ["REDESIGN_MEASUREMENT_ROOT"] = str(ROOT)
    spec = importlib.util.spec_from_file_location("q3_002_worker_grok", WORKER_PATH)
    worker = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(worker)
    return worker


def main() -> None:
    validation = preflight()
    worker = load_worker()
    worker.LEDGER_PATH = str(RUN / "budget-ledger.json")
    worker.CALLS_LOG = str(RUN / "model-calls.jsonl")
    worker.TRANSPORT_LOG = str(RUN / "transport-attempts.jsonl")
    worker.BUDGET = {
        "deepseek": {"target": 0.25, "hard": 0.40},
        "grok": {"target": 0.65, "hard": 0.85},
        "luna": {"target": 0.05, "hard": 0.10},
        "mission": {"target": 0.80, "hard": 1.25},
    }

    packet = (RUN / "deepseek-packet.yaml").read_text()
    ontology = (ROOT / "docs/canonical/q3-organizational-capability-ontology-v1.md").read_bytes()[415:11987].decode()
    runtime_rows = [
        json.loads(line) for line in
        (REPORT / "runtime-registration/runtime-universe.jsonl").read_text().splitlines()
        if line
    ]
    runtime_summary = "\n".join(
        f"- {row['schema']}.{row['table']}: rows={row['row_count']}"
        for row in runtime_rows
    )
    prompt = f"""
AUDIT ONLY — REFORMULATED-Q3-002

You may not collect evidence and have no tools. Audit the supplied DeepSeek packet
against Q3_ONTOLOGY_V1 and the external accounting facts. No previous Q3-001 count,
verdict, confidence, or expected number exists. A count in the packet is only a
derived property and is unsupported if any positive record fails Q1-Q6.

Frozen identity:
- subject: organizational_capabilities_implemented_by_this_repository
- relation: declared_or_configured_capability <-> surviving_runtime_evidence
- universe: registered source space + surviving runtime evidence universe
- required outputs: inventory, declaration/config provenance, runtime evidence,
  limitations, completeness accounting
- ontology normative hash: cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c

External deterministic facts:
- source registry: 1,060/1,060 tracked artifacts, hash a4a7bacbefb1d8f040f582e046a1ccdc0d2088b2c3ee2a42df65ec9d2ddfca43
- runtime registry: 103/103 accessible tables, hash b22d3801be5d8989d1a0025a2bedab7c5cb7656e12f912f12e2ffbc631f406e7
- DeepSeek accounted 70/70 navigation groups, but only 36/103 runtime relations.
- the missing 67 runtime units are all zero-row tables in the sealed snapshot;
  zero rows means no surviving observation, not historical mechanism absence.
- DeepSeek falsely phrases the runtime packet as though only 36 units were
  enumerated; externally all 103 were enumerated. Flag this unsupported statement.
- completeness is reserved to the external checker and DeepSeek correctly set
  coverage_complete=false.

Audit every positive capability record for: stable responsibility, causal purpose,
verifiable entry/transition, observable effect, Q5 supporting-artifact boundary,
Q6 layout independence, complete behavioral contract, and adjacent boundary.
Identify over-merged or over-split candidates and any runtime relation inferred
from artifact identity alone. Distinguish a valid partial inventory from a complete
inventory. Do not propose V2, expansion, deletion, or architecture changes.

Return ONLY valid YAML, no fence and no prose:
audit:
  question_id: REFORMULATED-Q3-002
  verdict: VALID_PARTIAL|VALID_COMPLETE|INVALID|UNMEASURABLE_AS_SPECIFIED|QUESTION_TARGET_DRIFT
  confidence: <0..1>
  observed_claims: []
  unsupported_inferences: []
  accepted_capability_ids: []
  challenged_capabilities:
    - capability_id: ...
      issue: ...
      ontology_rule: ...
  counterevidence: []
  measurement_limitations: []
  completeness_assessment: ...
  decision_relevance: ...
  falsifier: ...
  recommended_disposition: COMPLETE|PARTIAL|UNMEASURABLE_AS_SPECIFIED|QUESTION_TARGET_DRIFT

FROZEN NORMATIVE ONTOLOGY
---
{ontology}
---

EXTERNAL RUNTIME UNIT ACCOUNTING
---
{runtime_summary}
---

DEEPSEEK PACKET (verbatim normalized fence payload; hash {digest(RUN / 'deepseek-packet.yaml')})
---
{packet}
---
"""
    (RUN / "grok-prompt.txt").write_text(prompt)
    text, _ = worker.call_grok(
        prompt,
        task_id="REFORMULATED-Q3-002-AUDIT-001",
        question_id="REFORMULATED-Q3-002",
        max_tokens=6000,
    )
    (RUN / "grok-raw.txt").write_text(text)
    parsed = yaml.safe_load(text)
    audit = parsed.get("audit") if isinstance(parsed, dict) else None
    if not isinstance(audit, dict):
        raise SystemExit("STOP_AUDIT_GATE: missing audit")
    if audit.get("question_id") != "REFORMULATED-Q3-002":
        raise SystemExit("STOP_AUDIT_GATE: wrong question_id")
    required = {"verdict", "confidence", "observed_claims", "unsupported_inferences", "measurement_limitations", "decision_relevance", "falsifier", "recommended_disposition"}
    missing = sorted(required - set(audit))
    if missing:
        raise SystemExit(f"STOP_AUDIT_GATE: missing fields {missing}")
    result = {
        "question_id": audit["question_id"],
        "verdict": audit["verdict"],
        "confidence": audit["confidence"],
        "recommended_disposition": audit["recommended_disposition"],
        "accepted_capability_ids": len(audit.get("accepted_capability_ids") or []),
        "challenged_capabilities": len(audit.get("challenged_capabilities") or []),
        "raw_sha256": digest(RUN / "grok-raw.txt"),
        "ledger": worker.load_ledger(),
    }
    (RUN / "grok-result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
