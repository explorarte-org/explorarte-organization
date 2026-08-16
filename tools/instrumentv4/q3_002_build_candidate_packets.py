#!/usr/bin/env python3
"""Build deterministic navigation packets from already sealed Q3-002 universes.

Groups are search/navigation aids only.  They are explicitly not capabilities
and cannot be counted, merged, or split by directory identity.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path


def load_jsonl(path: Path) -> list[dict]:
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def group_for(path: str) -> str:
    parts = path.split("/")
    if parts[0] in {"internal", "cmd"} and len(parts) > 1:
        return "/".join(parts[:2])
    if parts[0] in {
        "empresa", "ingenieria_ia", "investigacion", "negocio",
        "recursos_agenticos", "servicios", "config", "migrations",
        "deployments", "scripts", "tools", ".github", "docs",
    }:
        return parts[0]
    return "root"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--source-registration", required=True, type=Path)
    parser.add_argument("--runtime-registration", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--compact-output", required=True, type=Path)
    args = parser.parse_args()

    sources = load_jsonl(args.source_registration / "source-universe.jsonl")
    symbols = load_jsonl(args.source_registration / "go-symbol-universe.jsonl")
    runtime = load_jsonl(args.runtime_registration / "runtime-universe.jsonl")
    categorical = load_jsonl(args.runtime_registration / "runtime-categorical-evidence.jsonl")
    by_group: dict[str, list[dict]] = {}
    for row in sources:
        by_group.setdefault(group_for(row["path"]), []).append(row)
    symbols_by_group: dict[str, list[dict]] = {}
    for row in symbols:
        symbols_by_group.setdefault(group_for(row["path"]), []).append(row)

    lines = [
        "# Q3-002 deterministic candidate navigation packets",
        "",
        "These groups are navigation aids, never inventory units. Package or",
        "directory identity MUST NOT be used as capability identity. Every path",
        "listed here remains independently registered in source-universe.jsonl.",
        "",
    ]
    for group in sorted(by_group):
        rows = by_group[group]
        kinds: dict[str, int] = {}
        for row in rows:
            kinds[row["registration_kind"]] = kinds.get(row["registration_kind"], 0) + 1
        lines.extend([
            f"## {group}",
            "",
            f"Tracked artifacts: {len(rows)}; kinds: " + ", ".join(f"{k}={v}" for k, v in sorted(kinds.items())),
            "",
        ])
        go_rows = [r for r in rows if r["path"].endswith(".go") and not r["path"].endswith("_test.go")]
        if go_rows:
            lines.append("Production Go paths:")
            for row in go_rows:
                lines.append(f"- `{row['path']}`")
            lines.append("")
        prod_symbols = [r for r in symbols_by_group.get(group, []) if not r["test_only_signal"]]
        if prod_symbols:
            lines.append("Function/method locators (mechanical index, not positive decisions):")
            for row in prod_symbols:
                recv = f"({row['receiver']})." if row["receiver"] else ""
                lines.append(f"- `{row['path']}:{row['line']}` — `{recv}{row['name']}`")
            lines.append("")
        nongos = [r for r in rows if not r["path"].endswith(".go")]
        if nongos:
            lines.append("Non-Go artifacts:")
            for row in nongos:
                lines.append(f"- `{row['path']}` ({row['registration_kind']})")
            lines.append("")

    lines.extend([
        "# Registered runtime universe summary",
        "",
        f"Accessible tables/views: {len(runtime)} (all registered; zero-row units retained).",
        "",
        "Non-empty runtime units:",
    ])
    for row in runtime:
        if row["row_count"]:
            lines.append(f"- `{row['schema']}.{row['table']}` — rows={row['row_count']}")
    lines.extend(["", "Registered categorical observations:"])
    for row in categorical:
        lines.append(
            f"- `{row['schema']}.{row['table']}.{row['column']}` = "
            f"`{row['value']}` — rows={row['row_count']}"
        )
    lines.append("")

    data = ("\n".join(lines) + "\n").encode()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(data)
    compact = [
        "# Q3-002 compact deterministic navigation index",
        "",
        "Navigation groups are not capabilities. No expected count exists.",
        "All 1,060 source units and all runtime units remain registered externally.",
        "",
    ]
    for group in sorted(by_group):
        rows = by_group[group]
        prod_paths = [r["path"] for r in rows if r["path"].endswith(".go") and not r["path"].endswith("_test.go")]
        prod_symbols = [r for r in symbols_by_group.get(group, []) if not r["test_only_signal"]]
        compact.append(f"## {group} — artifacts={len(rows)}, prod_go={len(prod_paths)}, symbols={len(prod_symbols)}")
        if prod_paths:
            compact.append("paths: " + ", ".join(f"`{p}`" for p in prod_paths))
        if prod_symbols:
            shown = prod_symbols[:40]
            compact.append("locators: " + ", ".join(
                f"`{r['path']}:{r['line']}:{r['name']}`" for r in shown
            ))
            if len(prod_symbols) > len(shown):
                compact.append(f"additional_symbol_count: {len(prod_symbols) - len(shown)}")
        compact.append("")
    compact.extend(["# Runtime", "", f"Registered units: {len(runtime)}"])
    for row in runtime:
        if row["row_count"]:
            compact.append(f"- `{row['schema']}.{row['table']}` rows={row['row_count']}")
    compact.append("")
    compact_data = ("\n".join(compact) + "\n").encode()
    args.compact_output.write_bytes(compact_data)
    print(json.dumps({
        "navigation_group_count": len(by_group),
        "source_units_referenced": sum(len(v) for v in by_group.values()),
        "runtime_units_referenced": len(runtime),
        "sha256": sha256(data),
        "bytes": len(data),
        "compact_sha256": sha256(compact_data),
        "compact_bytes": len(compact_data),
    }, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
