#!/usr/bin/env python3
"""Request the final ACCEPT/REJECT/DEFER disposition for Q3-002."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess


BASE = "588db11599d701fb1e2ecbae19aa00828663dc2b"
ROOT = Path("/home/ubuntu/campaign-588db11")
MEAS = Path("/home/ubuntu/q3-002-measurement-001")
RUN = MEAS / "docs/reports/q3-002-measurement-001/model-run"
FINAL = MEAS / "docs/reports/q3-002-measurement-001/final-accounting"
WORKER_PATH = Path("/home/ubuntu/redesign-001/worker.py")
COMPLETENESS_SHA = "37c26afabaaef9f2b2ba3f7143a3c9cefb28f3689f71204a2f29e5ffa9458667"


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_worker():
    os.environ["REDESIGN_MEASUREMENT_ROOT"] = str(ROOT)
    spec = importlib.util.spec_from_file_location("q3_002_worker_luna", WORKER_PATH)
    worker = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(worker)
    return worker


def main() -> None:
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
    status = subprocess.run(["git", "status", "--porcelain=v1"], cwd=ROOT, check=True, capture_output=True, text=True).stdout
    if head != BASE or status or digest(FINAL / "completeness-accounting.json") != COMPLETENESS_SHA:
        raise SystemExit("STOP_DISPOSITION_GATE: preflight failed")
    if (RUN / "luna-disposition.txt").exists():
        raise SystemExit("STOP: Luna disposition already exists")
    completeness = json.loads((FINAL / "completeness-accounting.json").read_text())
    initial_audit = json.loads((RUN / "grok-validation.json").read_text())
    refinement_audit = json.loads((RUN / "grok-refinement-result.json").read_text())
    prompt = f"""
QUESTION_ID: REFORMULATED-Q3-002

Return exactly one word: ACCEPT, REJECT, or DEFER.

Decide whether to accept the measurement artifact as an honest PARTIAL result,
not whether to claim completeness and not whether to authorize V2, expansion,
redesign, deletion, or another question.

Frozen question identity was preserved. Q3_ONTOLOGY_V1 was unchanged. Source and
runtime universes were registered before model calls. The question-identity gate
accepted the sole typed narrowing before its provider call. No expected count was
used. Completeness was computed externally, never self-attested by a model.

External accounting:
{json.dumps(completeness, indent=2, sort_keys=True)}

Initial Grok audit:
{json.dumps(initial_audit, indent=2, sort_keys=True)}

Typed-refinement Grok audit:
{json.dumps(refinement_audit, indent=2, sort_keys=True)}

Interpretation:
- ACCEPT = accept this as a valid PARTIAL measurement with explicit unknowns.
- REJECT = measurement is invalid (target drift, root/integrity failure, invalid
  positive records, or required output schema lost).
- DEFER = evidence does not yet support accepting or rejecting even the PARTIAL
  measurement.

Return exactly one word and nothing else.
"""
    (RUN / "luna-prompt.txt").write_text(prompt)
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
    text, _ = worker.call_luna(
        prompt,
        task_id="REFORMULATED-Q3-002-DISPOSITION-001",
        question_id="REFORMULATED-Q3-002",
    )
    disposition = text.strip()
    (RUN / "luna-raw.txt").write_text(text)
    if disposition not in {"ACCEPT", "REJECT", "DEFER"}:
        raise SystemExit(f"STOP_DISPOSITION_GATE: invalid Luna output {disposition!r}")
    (RUN / "luna-disposition.txt").write_text(disposition + "\n")
    result = {
        "question_id": "REFORMULATED-Q3-002",
        "luna_disposition": disposition,
        "measurement_disposition": completeness["measurement_disposition"],
        "raw_sha256": digest(RUN / "luna-raw.txt"),
        "ledger": worker.load_ledger(),
    }
    (RUN / "luna-result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
