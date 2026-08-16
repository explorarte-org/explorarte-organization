#!/usr/bin/env python3
"""Generate the non-self-referential provenance manifest for Q3-002."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
import subprocess


BASE = "588db11599d701fb1e2ecbae19aa00828663dc2b"
ROOT = Path("/home/ubuntu/campaign-588db11")
WORKTREE = Path("/home/ubuntu/q3-002-measurement-001")
REPORT_DIR = WORKTREE / "docs/reports/q3-002-measurement-001"
MANIFEST = WORKTREE / "docs/reports/q3-002-measurement-001.provenance.json"


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> None:
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
    clean = subprocess.run(["git", "status", "--porcelain=v1"], cwd=ROOT, check=True, capture_output=True, text=True).stdout == ""
    if head != BASE or not clean:
        raise SystemExit("scientific root identity failed")
    if MANIFEST.exists():
        raise SystemExit("manifest already exists")

    paths = [WORKTREE / "docs/reports/q3-002-measurement-001.md"]
    paths.extend(path for path in REPORT_DIR.rglob("*") if path.is_file())
    paths.extend(sorted((WORKTREE / "tools/instrumentv4").glob("q3_002_*.py")))
    paths = sorted(set(paths))
    artifacts = []
    for path in paths:
        artifacts.append({
            "path": str(path.relative_to(WORKTREE)),
            "bytes": path.stat().st_size,
            "sha256": sha256(path),
        })

    calls = [json.loads(line) for line in (REPORT_DIR / "model-run/model-calls.jsonl").read_text().splitlines() if line]
    role_counts = {}
    role_costs = {}
    for row in calls:
        role = row["role"]
        role_counts[role] = role_counts.get(role, 0) + 1
        role_costs[role] = role_costs.get(role, 0.0) + row["actual_cost"]
    trajectory = [json.loads(line) for line in (REPORT_DIR / "model-run/tool-trajectory.jsonl").read_text().splitlines() if line]
    invalid_roots = [row for row in trajectory if row["resolved_root"] != str(ROOT)]

    manifest = {
        "schema_version": "Q3_002_MEASUREMENT_PROVENANCE_V1",
        "mission_id": "ORGANIZATION-REDESIGN-001",
        "phase": "REFORMULATED_Q3_002_MEASUREMENT_001",
        "question_id": "REFORMULATED-Q3-002",
        "scientific_base_sha": BASE,
        "scientific_root": str(ROOT),
        "scientific_root_clean_at_freeze": clean,
        "artifact_worktree_branch": "measure/q3-002-measurement-001",
        "full_gate_lateral_commit": "1da6b8a4526bee015ff558c8b7e23117821424e3",
        "full_gate_merged_before_measurement": False,
        "ontology": {
            "version": "Q3_ONTOLOGY_V1",
            "whole_file_sha256": "0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974",
            "normative_sha256": "cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c",
        },
        "question_identity_contract_sha256": "5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c",
        "controller_sha256": "3547b61eba43b3aad6d14eec7313fdab1b480483c7d5d55b2368aba558066c8f",
        "gate_binary_sha256": "43b25561ed645231d4c74b499a99fc8c375aeb7ee947dcd86a9470e2a467f0cd",
        "source_universe_sha256": "a4a7bacbefb1d8f040f582e046a1ccdc0d2088b2c3ee2a42df65ec9d2ddfca43",
        "runtime_universe_sha256": "b22d3801be5d8989d1a0025a2bedab7c5cb7656e12f912f12e2ffbc631f406e7",
        "capability_inventory_sha256": "b81460712888bfb206bc1c60f9ed73bad003dff3606ad09848d448e5ddf71f98",
        "source_accounting_sha256": "858665f589aeeb02e9040fe00d4507cff6a58676e97fec06bc17e05c36662e42",
        "runtime_accounting_sha256": "679d8f7f201da7f77a7f26811f1141ecfd6d15e6c6c2f8d9e49f5715f4c29291",
        "completeness_accounting_sha256": "37c26afabaaef9f2b2ba3f7143a3c9cefb28f3689f71204a2f29e5ffa9458667",
        "measurement_disposition": "PARTIAL",
        "luna_disposition": "ACCEPT",
        "derived_inventory_size": 28,
        "expected_capability_count": None,
        "expected_count_used": False,
        "model_call_counts": dict(sorted(role_counts.items())),
        "model_costs_usd": dict(sorted(role_costs.items())),
        "total_model_cost_usd": sum(role_costs.values()),
        "tool_trajectory_records": len(trajectory),
        "invalid_tool_roots": invalid_roots,
        "production_database_role": "redesign_audit_readonly",
        "production_mutated": False,
        "question_expansion_executed": False,
        "v2_design_allowed": False,
        "manifest_hash_scope": "all listed artifact bytes; this manifest is excluded to avoid self-reference",
        "artifacts": artifacts,
    }
    MANIFEST.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    print(json.dumps({
        "artifact_count": len(artifacts),
        "manifest_sha256": sha256(MANIFEST),
        "model_call_counts": manifest["model_call_counts"],
        "total_model_cost_usd": manifest["total_model_cost_usd"],
        "tool_trajectory_records": manifest["tool_trajectory_records"],
        "invalid_tool_roots": len(invalid_roots),
    }, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
