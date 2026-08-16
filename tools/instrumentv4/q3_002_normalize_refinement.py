#!/usr/bin/env python3
"""Apply a narrow lexical YAML repair to the gated DeepSeek refinement.

Only list-item scalars beginning with the literal Go slice token ``[]`` are
JSON-quoted. The scalar's character sequence is otherwise unchanged.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import yaml


REQUIRED = {
    "capability_id", "name", "behavioral_contract", "causal_purpose",
    "entrypoints_or_triggers", "enforcement_or_transition_points",
    "reads_or_inputs", "writes_or_effects", "implementation_provenance",
    "declaration_provenance", "configuration_provenance",
    "possible_runtime_evidence", "boundary_with_adjacent_capabilities",
    "classification_basis",
}


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--raw", required=True, type=Path)
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--initial-packet", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--validation", required=True, type=Path)
    args = parser.parse_args()

    raw = args.raw.read_text()
    blocks = re.findall(r"```yaml\s*\n(.*?)\n```", raw, flags=re.DOTALL)
    if len(blocks) == 1:
        body = blocks[0]
        fence_rule = "sole YAML fence extracted verbatim"
    elif len(blocks) == 0:
        body = raw
        fence_rule = "raw response used"
    else:
        raise SystemExit("STOP_EVIDENCE_GATE: multiple YAML fences")

    repaired = []
    changes = []
    pattern = re.compile(r"^(\s*-\s+)(\[\].*)$")
    for line_no, line in enumerate(body.splitlines(), 1):
        match = pattern.match(line)
        if match:
            normalized = match.group(1) + json.dumps(match.group(2), ensure_ascii=False)
            changes.append({"line": line_no, "original": line, "normalized": normalized})
            repaired.append(normalized)
        else:
            repaired.append(line)
    payload = "\n".join(repaired)
    parsed = yaml.safe_load(payload)
    packet = parsed.get("refinement_packet") if isinstance(parsed, dict) else None
    if not isinstance(packet, dict):
        raise SystemExit("STOP_EVIDENCE_GATE: missing refinement_packet")
    if packet.get("question_id") != "REFORMULATED-Q3-002" or packet.get("profile") != "resolve_unclassified_source_entries_against_proposed_capabilities":
        raise SystemExit("STOP_EVIDENCE_GATE: identity/profile drift")
    if packet.get("coverage_complete") is not False:
        raise SystemExit("STOP_COMPLETENESS_SELF_ATTESTATION")

    initial = yaml.safe_load(args.initial_packet.read_text())["evidence_packet"]
    expected_groups = sorted(
        row["group"] for row in initial["navigation_group_accounting"]
        if row["classification"] == "UNRESOLVED"
    )
    rows = packet.get("resolved_group_accounting") or []
    actual_groups = [row.get("group") for row in rows if isinstance(row, dict)]
    if len(actual_groups) != len(set(actual_groups)) or set(actual_groups) != set(expected_groups):
        raise SystemExit("STOP_EVIDENCE_GATE: group accounting mismatch")
    allowed = {"RELEVANT_CAPABILITY_EVIDENCE", "IRRELEVANT_WITH_PROOF", "UNRESOLVED"}
    if any(row.get("classification") not in allowed for row in rows):
        raise SystemExit("STOP_EVIDENCE_GATE: invalid group label")

    cap_errors = []
    citations = []
    for cap in packet.get("new_capability_candidates") or []:
        missing = sorted(REQUIRED - set(cap))
        empty = [key for key in ("behavioral_contract", "causal_purpose", "entrypoints_or_triggers", "enforcement_or_transition_points", "implementation_provenance", "boundary_with_adjacent_capabilities", "classification_basis") if not cap.get(key)]
        if missing or empty:
            cap_errors.append({"capability_id": cap.get("capability_id"), "missing": missing, "empty": empty})
        for key in ("entrypoints_or_triggers", "enforcement_or_transition_points", "implementation_provenance", "declaration_provenance", "configuration_provenance"):
            citations.extend(x for x in cap.get(key) or [] if isinstance(x, str))
    if cap_errors:
        raise SystemExit(f"STOP_EVIDENCE_GATE: positive record errors {cap_errors}")

    tracked = set(subprocess.run(["git", "ls-files"], cwd=args.root, check=True, capture_output=True, text=True).stdout.splitlines())
    unresolved_citations = []
    for citation in citations:
        path = citation.split(":", 1)[0].strip("` ")
        if path not in tracked:
            unresolved_citations.append(citation)
    if unresolved_citations:
        raise SystemExit(f"STOP_EVIDENCE_GATE: unresolved citations {unresolved_citations}")

    args.output.write_text(payload)
    validation = {
        "fence_rule": fence_rule,
        "lexical_repair_rule": "JSON-quote list-item scalars beginning with literal [] only; preserve scalar characters",
        "lexical_changes": changes,
        "raw_sha256": sha256(args.raw.read_bytes()),
        "packet_sha256": sha256(args.output.read_bytes()),
        "evidence_gate_pass": True,
        "group_records": len(rows),
        "remaining_unresolved_groups": sum(1 for row in rows if row["classification"] == "UNRESOLVED"),
        "new_capability_candidates": len(packet.get("new_capability_candidates") or []),
        "citation_count": len(citations),
        "unresolved_citations": [],
        "coverage_complete_claim": False,
    }
    args.validation.write_text(json.dumps(validation, indent=2, sort_keys=True) + "\n")
    print(json.dumps(validation, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
