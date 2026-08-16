#!/usr/bin/env python3
"""Deterministically extract and validate a single fenced DeepSeek YAML packet.

This removes transport presentation only. It never edits, repairs, or adds any
semantic byte inside the YAML fence.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import yaml


REQUIRED_CAPABILITY_FIELDS = {
    "capability_id", "name", "behavioral_contract", "causal_purpose",
    "entrypoints_or_triggers", "enforcement_or_transition_points",
    "reads_or_inputs", "writes_or_effects", "implementation_provenance",
    "declaration_provenance", "configuration_provenance",
    "possible_runtime_evidence", "boundary_with_adjacent_capabilities",
    "classification_basis",
}
GROUP_LABELS = {"RELEVANT_CAPABILITY_EVIDENCE", "IRRELEVANT_WITH_PROOF", "UNRESOLVED"}
RUNTIME_LABELS = {"SURVIVING_EVIDENCE", "DECLARATION_OR_CONFIGURATION_ONLY", "IRRELEVANT_WITH_PROOF", "UNRESOLVED"}


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--raw", required=True, type=Path)
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--source-universe", required=True, type=Path)
    parser.add_argument("--runtime-universe", required=True, type=Path)
    parser.add_argument("--candidate-index", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--validation", required=True, type=Path)
    args = parser.parse_args()

    raw = args.raw.read_text()
    matches = re.findall(r"```yaml\s*\n(.*?)\n```", raw, flags=re.DOTALL)
    if len(matches) != 1:
        raise SystemExit(f"STOP_EVIDENCE_GATE: expected one YAML fence, found {len(matches)}")
    packet_text = matches[0]
    packet_bytes = packet_text.encode()
    parsed = yaml.safe_load(packet_text)
    packet = parsed.get("evidence_packet") if isinstance(parsed, dict) else None
    if not isinstance(packet, dict):
        raise SystemExit("STOP_EVIDENCE_GATE: missing evidence_packet")
    if packet.get("question_id") != "REFORMULATED-Q3-002":
        raise SystemExit("STOP_EVIDENCE_GATE: wrong question_id")
    if not packet.get("observations") or not packet.get("provenance"):
        raise SystemExit("STOP_EVIDENCE_GATE: empty observations/provenance")
    if packet.get("coverage_complete") is not False:
        raise SystemExit("STOP_COMPLETENESS_SELF_ATTESTATION")

    capabilities = packet.get("capability_candidates")
    if not isinstance(capabilities, list):
        raise SystemExit("STOP_EVIDENCE_GATE: capability_candidates is not a list")
    capability_errors = []
    for idx, cap in enumerate(capabilities):
        missing = sorted(REQUIRED_CAPABILITY_FIELDS - set(cap)) if isinstance(cap, dict) else sorted(REQUIRED_CAPABILITY_FIELDS)
        empty_required = []
        if isinstance(cap, dict):
            for key in ("behavioral_contract", "causal_purpose", "entrypoints_or_triggers", "enforcement_or_transition_points", "implementation_provenance", "boundary_with_adjacent_capabilities", "classification_basis"):
                if not cap.get(key):
                    empty_required.append(key)
        if missing or empty_required:
            capability_errors.append({"index": idx, "missing": missing, "empty_required": empty_required})

    expected_groups = re.findall(r"^## (.+?) — artifacts=", args.candidate_index.read_text(), flags=re.MULTILINE)
    group_rows = packet.get("navigation_group_accounting") or []
    actual_groups = [row.get("group") for row in group_rows if isinstance(row, dict)]
    group_label_errors = [row for row in group_rows if not isinstance(row, dict) or row.get("classification") not in GROUP_LABELS]

    runtime_rows_expected = [json.loads(line) for line in args.runtime_universe.read_text().splitlines() if line]
    expected_runtime = [f"{row['schema']}.{row['table']}" for row in runtime_rows_expected]
    runtime_rows = packet.get("runtime_relations") or []
    actual_runtime = [row.get("runtime_unit") for row in runtime_rows if isinstance(row, dict)]
    runtime_label_errors = [row for row in runtime_rows if not isinstance(row, dict) or row.get("relation") not in RUNTIME_LABELS]

    tracked = set(subprocess.run(
        ["git", "ls-files"], cwd=args.root, check=True, capture_output=True, text=True,
    ).stdout.splitlines())
    citations = []
    for cap in capabilities:
        if not isinstance(cap, dict):
            continue
        for key in ("entrypoints_or_triggers", "enforcement_or_transition_points", "implementation_provenance", "declaration_provenance", "configuration_provenance"):
            for value in cap.get(key) or []:
                if isinstance(value, str):
                    citations.append(value)
    citations.extend(value for value in packet.get("provenance") or [] if isinstance(value, str))
    unresolved_citations = []
    for citation in citations:
        path = citation.split(":", 1)[0].strip("` ")
        if path.startswith("public."):
            continue
        if path not in tracked:
            unresolved_citations.append(citation)

    source_count = sum(1 for line in args.source_universe.read_text().splitlines() if line)
    validation = {
        "normalization_rule": "extract the sole ```yaml fenced payload verbatim; no semantic edits",
        "raw_sha256": sha256(args.raw.read_bytes()),
        "normalized_packet_sha256": sha256(packet_bytes),
        "question_id_valid": True,
        "observations_nonempty": True,
        "provenance_nonempty": True,
        "coverage_complete_claim": False,
        "source_universe_count": source_count,
        "positive_capability_records": len(capabilities),
        "positive_record_errors": capability_errors,
        "expected_navigation_groups": len(expected_groups),
        "reported_navigation_groups": len(actual_groups),
        "duplicate_navigation_groups": sorted({x for x in actual_groups if x and actual_groups.count(x) > 1}),
        "missing_navigation_groups": sorted(set(expected_groups) - set(actual_groups)),
        "foreign_navigation_groups": sorted(set(actual_groups) - set(expected_groups)),
        "navigation_label_errors": group_label_errors,
        "expected_runtime_units": len(expected_runtime),
        "reported_runtime_units": len(actual_runtime),
        "duplicate_runtime_units": sorted({x for x in actual_runtime if x and actual_runtime.count(x) > 1}),
        "missing_runtime_units": sorted(set(expected_runtime) - set(actual_runtime)),
        "foreign_runtime_units": sorted(set(actual_runtime) - set(expected_runtime)),
        "runtime_label_errors": runtime_label_errors,
        "citation_count": len(citations),
        "unresolved_citations": unresolved_citations,
    }
    validation["structural_evidence_gate_pass"] = not (
        capability_errors or group_label_errors or runtime_label_errors or unresolved_citations
    )
    args.output.write_bytes(packet_bytes)
    args.validation.write_text(json.dumps(validation, indent=2, sort_keys=True) + "\n")
    print(json.dumps(validation, indent=2, sort_keys=True))
    if not validation["structural_evidence_gate_pass"]:
        raise SystemExit("STOP_EVIDENCE_GATE: structural validation failed")


if __name__ == "__main__":
    main()
