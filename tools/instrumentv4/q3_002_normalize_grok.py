#!/usr/bin/env python3
"""Extract the sole fenced Grok audit without semantic modification."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import yaml


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--raw", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--validation", required=True, type=Path)
    args = parser.parse_args()
    raw = args.raw.read_text()
    blocks = re.findall(r"```yaml\s*\n(.*?)\n```", raw, flags=re.DOTALL)
    if len(blocks) != 1:
        raise SystemExit(f"STOP_AUDIT_GATE: expected one YAML fence, found {len(blocks)}")
    payload = blocks[0]
    parsed = yaml.safe_load(payload)
    audit = parsed.get("audit") if isinstance(parsed, dict) else None
    if not isinstance(audit, dict) or audit.get("question_id") != "REFORMULATED-Q3-002":
        raise SystemExit("STOP_AUDIT_GATE: invalid audit identity")
    required = {"verdict", "confidence", "observed_claims", "unsupported_inferences", "accepted_capability_ids", "challenged_capabilities", "counterevidence", "measurement_limitations", "completeness_assessment", "decision_relevance", "falsifier", "recommended_disposition"}
    missing = sorted(required - set(audit))
    if missing:
        raise SystemExit(f"STOP_AUDIT_GATE: missing {missing}")
    if audit["verdict"] not in {"VALID_PARTIAL", "VALID_COMPLETE", "INVALID", "UNMEASURABLE_AS_SPECIFIED", "QUESTION_TARGET_DRIFT"}:
        raise SystemExit("STOP_AUDIT_GATE: invalid verdict")
    if audit["recommended_disposition"] not in {"COMPLETE", "PARTIAL", "UNMEASURABLE_AS_SPECIFIED", "QUESTION_TARGET_DRIFT"}:
        raise SystemExit("STOP_AUDIT_GATE: invalid disposition")
    data = payload.encode()
    args.output.write_bytes(data)
    validation = {
        "normalization_rule": "extract the sole ```yaml fenced payload verbatim; no semantic edits",
        "raw_sha256": hashlib.sha256(args.raw.read_bytes()).hexdigest(),
        "normalized_sha256": hashlib.sha256(data).hexdigest(),
        "audit_gate_pass": True,
        "verdict": audit["verdict"],
        "confidence": audit["confidence"],
        "recommended_disposition": audit["recommended_disposition"],
        "accepted_capability_count": len(audit["accepted_capability_ids"]),
        "challenged_capability_count": len(audit["challenged_capabilities"]),
    }
    args.validation.write_text(json.dumps(validation, indent=2, sort_keys=True) + "\n")
    print(json.dumps(validation, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
