#!/usr/bin/env python3
"""Register the complete Git-tracked source space for REFORMULATED-Q3-002.

This script is deliberately classification-free.  It seals the universe before
any model sees it; later accounting must classify every registered source unit
as capability evidence, irrelevant-with-proof, or unresolved.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


EXPECTED_SHA = "588db11599d701fb1e2ecbae19aa00828663dc2b"
QUESTION_ID = "REFORMULATED-Q3-002"
GO_FUNC = re.compile(
    rb"^func\s+(?:\((?P<receiver>[^)]*)\)\s*)?"
    rb"(?P<name>[A-Za-z_][A-Za-z0-9_]*)\s*\(",
    re.MULTILINE,
)


def run(root: Path, *args: str, binary: bool = False) -> bytes | str:
    result = subprocess.run(
        list(args), cwd=root, check=True, capture_output=True,
        text=not binary,
    )
    return result.stdout


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def artifact_kind(path: str) -> str:
    name = Path(path).name
    suffix = Path(path).suffix.lower()
    if path.endswith("_test.go") or "/testdata/" in f"/{path}" or re.search(r"(^|/)[^/]*fixtures?(/|$)", path):
        return "test_only_signal"
    if path.startswith("migrations/") and suffix == ".sql":
        return "migration"
    if suffix == ".go":
        return "go_source"
    if suffix in {".yaml", ".yml", ".json", ".toml", ".example"} or name in {"AGENT.md", "PERFIL.md"}:
        return "configuration_or_declaration"
    if suffix in {".md", ".pdf", ".tsv"}:
        return "documentation_or_data"
    if suffix in {".sh", ".py"} or path.startswith("scripts/") or path.startswith("tools/"):
        return "supporting_script_signal"
    if name in {"Dockerfile", "Makefile", "compose.yaml", "compose.integration.yaml"} or path.startswith("deployments/") or path.startswith(".github/"):
        return "deployment_or_build_infrastructure"
    if name in {"go.mod", "go.sum"}:
        return "external_dependency_declaration"
    return "other"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    root = args.root.resolve()
    out = args.output_dir.resolve()

    head = str(run(root, "git", "rev-parse", "HEAD")).strip()
    if head != EXPECTED_SHA:
        raise SystemExit(f"wrong HEAD: {head}")
    if str(run(root, "git", "status", "--porcelain=v1")):
        raise SystemExit("measurement root is dirty")

    raw_paths = run(root, "git", "ls-files", "-z", binary=True)
    assert isinstance(raw_paths, bytes)
    paths = [p.decode("utf-8") for p in raw_paths.split(b"\0") if p]
    if len(paths) != len(set(paths)) or paths != sorted(paths):
        raise SystemExit("git path list is not unique and sorted")

    staged = str(run(root, "git", "ls-files", "--stage"))
    index = {}
    for line in staged.splitlines():
        metadata, path = line.split("\t", 1)
        mode, oid, stage = metadata.split()
        if stage != "0":
            raise SystemExit(f"nonzero index stage: {path}")
        index[path] = {"git_mode": mode, "git_blob_oid": oid}

    source_units = []
    symbols = []
    for ordinal, path in enumerate(paths, 1):
        full = (root / path).resolve()
        if not str(full).startswith(str(root) + os.sep):
            raise SystemExit(f"path escapes root: {path}")
        data = full.read_bytes()
        unit_id = "SRC-" + sha256(path.encode("utf-8"))[:16].upper()
        kind = artifact_kind(path)
        source_units.append({
            "source_unit_id": unit_id,
            "ordinal": ordinal,
            "path": path,
            **index[path],
            "sha256": sha256(data),
            "bytes": len(data),
            "top_level": path.split("/", 1)[0],
            "extension": Path(path).suffix.lower(),
            "registration_kind": kind,
            "final_accounting": "PENDING",
        })
        if path.endswith(".go"):
            for match in GO_FUNC.finditer(data):
                line = data.count(b"\n", 0, match.start()) + 1
                receiver = (match.group("receiver") or b"").decode("utf-8", "replace").strip()
                name = match.group("name").decode("ascii")
                symbols.append({
                    "symbol_id": "SYM-" + sha256(f"{path}:{line}:{receiver}:{name}".encode())[:16].upper(),
                    "source_unit_id": unit_id,
                    "path": path,
                    "line": line,
                    "receiver": receiver,
                    "name": name,
                    "test_only_signal": kind == "test_only_signal",
                })

    out.mkdir(parents=True, exist_ok=False)
    source_bytes = b"".join(
        (json.dumps(row, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
        for row in source_units
    )
    symbol_bytes = b"".join(
        (json.dumps(row, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
        for row in symbols
    )
    (out / "source-universe.jsonl").write_bytes(source_bytes)
    (out / "go-symbol-universe.jsonl").write_bytes(symbol_bytes)

    kinds = {}
    for row in source_units:
        kinds[row["registration_kind"]] = kinds.get(row["registration_kind"], 0) + 1
    manifest = {
        "phase": "REFORMULATED_Q3_002_MEASUREMENT_001",
        "question_id": QUESTION_ID,
        "measurement_root": str(root),
        "measurement_sha": head,
        "root_clean": True,
        "registration_algorithm": "git ls-files -z; exact tracked bytes; one source unit per tracked path; no semantic classification",
        "source_unit_count": len(source_units),
        "go_symbol_count": len(symbols),
        "registration_kind_counts": dict(sorted(kinds.items())),
        "source_universe_sha256": sha256(source_bytes),
        "go_symbol_universe_sha256": sha256(symbol_bytes),
        "model_calls_before_registration": 0,
        "expected_capability_count": None,
    }
    manifest_bytes = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode("utf-8")
    (out / "source-registration-manifest.json").write_bytes(manifest_bytes)
    print(json.dumps({**manifest, "manifest_sha256": sha256(manifest_bytes)}, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
