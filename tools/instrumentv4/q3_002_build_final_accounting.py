#!/usr/bin/env python3
"""Build external Q3-002 inventory and no-omission completeness accounting."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import yaml


def load_jsonl(path: Path) -> list[dict]:
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def encode_jsonl(rows: list[dict]) -> bytes:
    return b"".join((json.dumps(row, sort_keys=True, separators=(",", ":")) + "\n").encode() for row in rows)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def group_for(path: str) -> str:
    parts = path.split("/")
    if parts[0] in {"internal", "cmd"} and len(parts) > 1:
        return "/".join(parts[:2])
    if parts[0] in {"empresa", "ingenieria_ia", "investigacion", "negocio", "recursos_agenticos", "servicios", "config", "migrations", "deployments", "scripts", "tools", ".github", "docs"}:
        return parts[0]
    return "root"


def explicitly_unresolved(path: str) -> str | None:
    rules = [
        (r"^internal/corpuscuration/.*identity_preflight.*\.go$", "refinement explicitly left identity-preflight behavior unresolved"),
        (r"^internal/corpuscuration/.*gaps.*\.go$", "refinement explicitly left knowledge-profile gap behavior unresolved"),
        (r"^internal/corpuscuration/.*store.*\.go$", "refinement explicitly left curation store boundary unresolved"),
        (r"^internal/evaluation/(postgres|metrics)/", "refinement/audit did not inspect evaluation persistence/metrics fully"),
        (r"^internal/embeddingruntime/adapter/gemini/", "refinement explicitly left Gemini request/idempotency flow partially observed"),
    ]
    for pattern, reason in rules:
        if re.search(pattern, path):
            return reason
    return None


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report-dir", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    report = args.report_dir
    out = args.output_dir
    if out.exists():
        raise SystemExit("output directory already exists")

    initial = yaml.safe_load((report / "model-run/deepseek-packet.yaml").read_text())["evidence_packet"]
    initial_audit = yaml.safe_load((report / "model-run/grok-audit.yaml").read_text())["audit"]
    refinement = yaml.safe_load((report / "model-run/deepseek-refinement-packet.yaml").read_text())["refinement_packet"]
    refinement_audit = yaml.safe_load((report / "model-run/grok-refinement-audit.yaml").read_text())["refinement_audit"]
    source_units = load_jsonl(report / "source-registration/source-universe.jsonl")
    runtime_units = load_jsonl(report / "runtime-registration/runtime-universe.jsonl")

    initial_caps = {cap["capability_id"]: cap for cap in initial["capability_candidates"]}
    accepted_initial = set(initial_audit["accepted_capability_ids"])
    new_caps = {cap["capability_id"]: cap for cap in refinement["new_capability_candidates"]}
    accepted_new = set(refinement_audit["accepted_new_capability_ids"])
    if accepted_initial - set(initial_caps) or accepted_new - set(new_caps):
        raise SystemExit("audit accepted an unknown capability")
    if accepted_initial & accepted_new:
        raise SystemExit("duplicate capability ID across passes")

    challenge_map = {}
    for row in initial_audit.get("challenged_capabilities") or []:
        challenge_map[row["capability_id"]] = row
    for row in refinement_audit.get("challenged_new_capabilities") or []:
        challenge_map[row["capability_id"]] = row

    inventory = []
    for capability_id in sorted(accepted_initial | accepted_new):
        record = dict(initial_caps.get(capability_id) or new_caps[capability_id])
        record["measurement_classification"] = "ORGANIZATIONAL_CAPABILITY"
        record["audit_status"] = "ACCEPTED_WITH_CHALLENGE" if capability_id in challenge_map else "ACCEPTED"
        record["audit_challenge"] = challenge_map.get(capability_id)
        record["count_semantics"] = "derived inventory member; never an expected count"
        inventory.append(record)

    rejected = refinement_audit["rejected_new_capabilities"]
    rejected_map = {row["capability_id"]: row for row in rejected}
    supporting_target = {}
    for cid, row in rejected_map.items():
        terminal = row["terminal_classification"]
        if terminal.startswith("SUPPORTING_ARTIFACT_OF:"):
            supporting_target[cid] = terminal.split(":", 1)[1]

    groups = {row["group"]: dict(row) for row in initial["navigation_group_accounting"]}
    for row in refinement["resolved_group_accounting"]:
        groups[row["group"]] = dict(row)
    if len(groups) != 70:
        raise SystemExit(f"expected 70 navigation groups, got {len(groups)}")

    source_accounting = []
    for unit in source_units:
        path = unit["path"]
        group = group_for(path)
        row = groups[group]
        unknown_reason = explicitly_unresolved(path)
        capability_ids = list(row.get("capability_ids") or [])
        mapped = []
        for cid in capability_ids:
            mapped.append(supporting_target.get(cid, cid))
        mapped = sorted(set(mapped))
        if unknown_reason:
            category = "UNRESOLVED"
            mapped = []
            reason = unknown_reason
        elif row["classification"] == "RELEVANT_CAPABILITY_EVIDENCE":
            category = "CAPABILITY_EVIDENCE"
            reason = row["reason"]
        elif row["classification"] == "IRRELEVANT_WITH_PROOF":
            category = "IRRELEVANT_WITH_PROOF"
            reason = row["reason"]
            mapped = []
        else:
            category = "UNRESOLVED"
            reason = row["reason"]
            mapped = []
        source_accounting.append({
            **unit,
            "navigation_group": group,
            "final_accounting": category,
            "capability_ids": mapped,
            "accounting_reason": reason,
            "accounting_provenance": row.get("provenance") or [],
        })

    runtime_relations = {row["runtime_unit"]: dict(row) for row in initial["runtime_relations"]}
    runtime_accounting = []
    for unit in runtime_units:
        name = f"{unit['schema']}.{unit['table']}"
        relation = runtime_relations.get(name)
        if relation:
            final_relation = relation["relation"]
            capability_ids = sorted(set(relation.get("capability_ids") or []))
            reason = relation["reason"]
        else:
            if unit["row_count"] != 0:
                raise SystemExit(f"model omitted nonempty runtime unit: {name}")
            final_relation = "UNRESOLVED"
            capability_ids = []
            reason = "enumerated zero-row unit; no surviving observation; no inference of historical absence"
        runtime_accounting.append({
            **unit,
            "final_accounting": final_relation,
            "capability_ids": capability_ids,
            "accounting_reason": reason,
        })

    source_counts = {key: sum(1 for row in source_accounting if row["final_accounting"] == key) for key in ("CAPABILITY_EVIDENCE", "IRRELEVANT_WITH_PROOF", "UNRESOLVED")}
    runtime_labels = sorted(set(row["final_accounting"] for row in runtime_accounting))
    runtime_counts = {key: sum(1 for row in runtime_accounting if row["final_accounting"] == key) for key in runtime_labels}
    source_total = sum(source_counts.values())
    runtime_total = sum(runtime_counts.values())
    if source_total != len(source_units) or runtime_total != len(runtime_units):
        raise SystemExit("implicit omission detected")
    if source_counts["UNRESOLVED"] == 0 and runtime_counts.get("UNRESOLVED", 0) == 0:
        raise SystemExit("unexpectedly complete accounting; manual review required")

    out.mkdir(parents=True, exist_ok=False)
    inventory_doc = {
        "question_id": "REFORMULATED-Q3-002",
        "ontology_version": "Q3_ONTOLOGY_V1",
        "ontology_whole_file_sha256": "0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974",
        "ontology_normative_sha256": "cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c",
        "expected_capability_count": None,
        "derived_inventory_size": len(inventory),
        "capabilities": inventory,
        "non_positive_candidates": rejected,
    }
    inventory_bytes = yaml.safe_dump(inventory_doc, sort_keys=False, allow_unicode=True).encode()
    source_bytes = encode_jsonl(source_accounting)
    runtime_bytes = encode_jsonl(runtime_accounting)
    (out / "capability-inventory.yaml").write_bytes(inventory_bytes)
    (out / "source-accounting.jsonl").write_bytes(source_bytes)
    (out / "runtime-accounting.jsonl").write_bytes(runtime_bytes)

    completeness = {
        "question_id": "REFORMULATED-Q3-002",
        "measurement_disposition": "PARTIAL",
        "reason": "required output schema preserved and every registered unit accounted, but explicit unresolved source/runtime units remain",
        "source_registered": len(source_units),
        "source_accounted": source_total,
        "source_omitted": len(source_units) - source_total,
        "source_counts": source_counts,
        "runtime_registered": len(runtime_units),
        "runtime_accounted": runtime_total,
        "runtime_omitted": len(runtime_units) - runtime_total,
        "runtime_counts": runtime_counts,
        "derived_inventory_size": len(inventory),
        "accepted_with_challenge": sum(1 for row in inventory if row["audit_status"] == "ACCEPTED_WITH_CHALLENGE"),
        "non_positive_candidates": len(rejected),
        "expected_capability_count": None,
        "expected_count_used": False,
        "completeness_self_attested_by_model": False,
        "zero_runtime_observations_imply_historical_absence": False,
        "missing_evidence_implies_missing_capability": False,
        "question_target_drift": False,
        "source_accounting_sha256": sha256(source_bytes),
        "runtime_accounting_sha256": sha256(runtime_bytes),
        "capability_inventory_sha256": sha256(inventory_bytes),
    }
    completeness_bytes = (json.dumps(completeness, indent=2, sort_keys=True) + "\n").encode()
    (out / "completeness-accounting.json").write_bytes(completeness_bytes)
    print(json.dumps({**completeness, "completeness_sha256": sha256(completeness_bytes)}, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
