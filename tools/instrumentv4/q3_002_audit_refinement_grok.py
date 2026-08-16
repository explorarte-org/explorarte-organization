#!/usr/bin/env python3
"""Audit material capability candidates from the gated source refinement."""

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
RUN = MEAS / "docs/reports/q3-002-measurement-001/model-run"
WORKER_PATH = Path("/home/ubuntu/redesign-001/worker.py")


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_worker():
    os.environ["REDESIGN_MEASUREMENT_ROOT"] = str(ROOT)
    spec = importlib.util.spec_from_file_location("q3_002_worker_refine_audit", WORKER_PATH)
    worker = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(worker)
    return worker


def main() -> None:
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
    status = subprocess.run(["git", "status", "--porcelain=v1"], cwd=ROOT, check=True, capture_output=True, text=True).stdout
    validation = json.loads((RUN / "deepseek-refinement-validation.json").read_text())
    if head != BASE or status or not validation.get("evidence_gate_pass"):
        raise SystemExit("STOP_AUDIT_GATE: preflight failed")
    if (RUN / "grok-refinement-raw.txt").exists():
        raise SystemExit("STOP: refinement audit already exists")

    ontology = (ROOT / "docs/canonical/q3-organizational-capability-ontology-v1.md").read_bytes()[415:11987].decode()
    packet = (RUN / "deepseek-refinement-packet.yaml").read_text()
    initial_audit = yaml.safe_load((RUN / "grok-audit.yaml").read_text())["audit"]
    prompt = f"""
SECOND AUDIT — MATERIAL TYPED REFINEMENT OF REFORMULATED-Q3-002

Audit only the supplied gated refinement. It used the allowlisted profile
resolve_unclassified_source_entries_against_proposed_capabilities and retained
the frozen subject/relation/universes/output schema. The gate returned
ACCEPT_IDENTITY_PRESERVED before the provider call. No tools are available.

The initial audit was VALID_PARTIAL and accepted 21 proposed capabilities while
challenging four boundaries. The refinement accounts for the 23 formerly
UNRESOLVED navigation groups, proposes 14 new positive records, and leaves zero
groups unresolved. This is material new evidence, but completeness remains an
external property and runtime has 67 zero-row units with no surviving evidence.

Apply Q1-Q6 strictly to each of the 14 new candidates. Pay special attention to:
- helpers/algorithms versus autonomous operational responsibility;
- generic adapters/infrastructure versus organizational behavior (Q5);
- external services versus repository-owned orchestration (Q1);
- test/evaluation/security-audit tooling relevance to organization behavior;
- over-splitting corpus processing into independent capabilities;
- questionidentity as implemented repository behavior, not merely a package/CLI;
- behavioral contracts and adjacent boundaries.

Return ONLY valid YAML (a sole YAML fence is tolerated as transport formatting):
refinement_audit:
  question_id: REFORMULATED-Q3-002
  verdict: VALID_PARTIAL|VALID_COMPLETE|INVALID|QUESTION_TARGET_DRIFT
  confidence: <0..1>
  accepted_new_capability_ids: []
  rejected_new_capabilities:
    - capability_id: ...
      terminal_classification: NOT_A_CAPABILITY|SUPPORTING_ARTIFACT_OF:<id>|UNRESOLVED
      reason: ...
      ontology_rule: ...
  challenged_new_capabilities:
    - capability_id: ...
      issue: ...
      ontology_rule: ...
  observed_claims: []
  unsupported_inferences: []
  measurement_limitations: []
  completeness_assessment: ...
  recommended_disposition: COMPLETE|PARTIAL|UNMEASURABLE_AS_SPECIFIED|QUESTION_TARGET_DRIFT
  falsifier: ...

FROZEN NORMATIVE ONTOLOGY
---
{ontology}
---

INITIAL AUDIT SUMMARY
---
verdict: {initial_audit['verdict']}
accepted_capability_ids: {json.dumps(initial_audit['accepted_capability_ids'])}
challenged_capabilities: {json.dumps(initial_audit['challenged_capabilities'])}
---

GATED REFINEMENT PACKET (hash {digest(RUN / 'deepseek-refinement-packet.yaml')})
---
{packet}
---
"""
    (RUN / "grok-refinement-prompt.txt").write_text(prompt)
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
    text, _ = worker.call_grok(
        prompt,
        task_id="Q3-002-AUDIT-RESOLVED-SOURCE-ENTRIES",
        question_id="REFORMULATED-Q3-002",
        max_tokens=5000,
    )
    (RUN / "grok-refinement-raw.txt").write_text(text)
    blocks = re.findall(r"```yaml\s*\n(.*?)\n```", text, flags=re.DOTALL)
    if len(blocks) == 1:
        payload = blocks[0]
        normalization = "sole YAML fence extracted verbatim"
    elif not blocks:
        payload = text
        normalization = "raw response already YAML"
    else:
        raise SystemExit("STOP_AUDIT_GATE: multiple YAML fences")
    parsed = yaml.safe_load(payload)
    audit = parsed.get("refinement_audit") if isinstance(parsed, dict) else None
    if not isinstance(audit, dict) or audit.get("question_id") != "REFORMULATED-Q3-002":
        raise SystemExit("STOP_AUDIT_GATE: invalid refinement audit")
    required = {"verdict", "confidence", "accepted_new_capability_ids", "rejected_new_capabilities", "challenged_new_capabilities", "observed_claims", "unsupported_inferences", "measurement_limitations", "completeness_assessment", "recommended_disposition", "falsifier"}
    missing = sorted(required - set(audit))
    if missing:
        raise SystemExit(f"STOP_AUDIT_GATE: missing fields {missing}")
    (RUN / "grok-refinement-audit.yaml").write_text(payload)
    result = {
        "normalization": normalization,
        "verdict": audit["verdict"],
        "confidence": audit["confidence"],
        "accepted_new_capabilities": len(audit["accepted_new_capability_ids"]),
        "rejected_new_capabilities": len(audit["rejected_new_capabilities"]),
        "challenged_new_capabilities": len(audit["challenged_new_capabilities"]),
        "recommended_disposition": audit["recommended_disposition"],
        "audit_sha256": digest(RUN / "grok-refinement-audit.yaml"),
        "ledger": worker.load_ledger(),
    }
    (RUN / "grok-refinement-result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
