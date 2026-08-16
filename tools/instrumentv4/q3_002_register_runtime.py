#!/usr/bin/env python3
"""Register the production runtime evidence universe using read-only SQL.

The catalog is enumerated first.  Counts and pre-registered categorical
observations are then collected in one REPEATABLE READ, READ ONLY transaction.
No table, temporary object, function, or database state is created.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess


EXPECTED_SHA = "588db11599d701fb1e2ecbae19aa00828663dc2b"
EXPECTED_ROLE = "redesign_audit_readonly"
EXPECTED_DATABASE = "explorarte_org"
CONTAINER = "explorarte-organization-postgres-1"
CATEGORICAL_ALLOWLIST = {
    "event_type", "status", "decision_status", "role", "role_id",
    "provider", "channel", "kind", "invocation_type", "capability",
    "capability_id", "task_type", "message_type", "operation_type",
}
SAFE_IDENT = re.compile(r"^[a-z_][a-z0-9_]*$")


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def git(root: Path, *args: str) -> str:
    return subprocess.run(
        ["git", *args], cwd=root, check=True, capture_output=True, text=True,
    ).stdout


def psql(sql: str) -> list[str]:
    result = subprocess.run(
        [
            "docker", "exec", "-i", CONTAINER, "psql",
            "-U", EXPECTED_ROLE, "-d", EXPECTED_DATABASE,
            "-v", "ON_ERROR_STOP=1", "-qAt", "-c", sql,
        ],
        check=True, capture_output=True, text=True, timeout=180,
    )
    return [line for line in result.stdout.splitlines() if line]


def literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def ident(value: str) -> str:
    if not SAFE_IDENT.fullmatch(value):
        raise SystemExit(f"unsafe catalog identifier: {value!r}")
    return '"' + value + '"'


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    root = args.root.resolve()
    out = args.output_dir.resolve()

    head = git(root, "rev-parse", "HEAD").strip()
    if head != EXPECTED_SHA:
        raise SystemExit(f"wrong HEAD: {head}")
    if git(root, "status", "--porcelain=v1"):
        raise SystemExit("measurement root is dirty")

    identity_sql = """
    SELECT json_build_object(
      'role', current_user,
      'database', current_database(),
      'transaction_read_only', current_setting('transaction_read_only'),
      'default_transaction_read_only', current_setting('default_transaction_read_only'),
      'statement_timeout', current_setting('statement_timeout'),
      'database_create', has_database_privilege(current_user,current_database(),'CREATE'),
      'database_temp', has_database_privilege(current_user,current_database(),'TEMP'),
      'public_schema_create', has_schema_privilege(current_user,'public','CREATE'),
      'public_schema_usage', has_schema_privilege(current_user,'public','USAGE')
    )::text;
    """
    identity_lines = psql(identity_sql)
    if len(identity_lines) != 1:
        raise SystemExit("database identity query did not return exactly one row")
    identity = json.loads(identity_lines[0])
    required = {
        "role": EXPECTED_ROLE,
        "database": EXPECTED_DATABASE,
        "transaction_read_only": "on",
        "default_transaction_read_only": "on",
        "database_create": False,
        "public_schema_create": False,
        "public_schema_usage": True,
    }
    for key, value in required.items():
        if identity.get(key) != value:
            raise SystemExit(f"read-only identity failure: {key}={identity.get(key)!r}")

    grants_sql = """
    SELECT json_build_object('privilege_type', privilege_type, 'grants', count(*))::text
    FROM information_schema.role_table_grants
    WHERE grantee=current_user
    GROUP BY privilege_type ORDER BY privilege_type;
    """
    grants = [json.loads(line) for line in psql(grants_sql)]
    if not grants or any(row["privilege_type"] != "SELECT" for row in grants):
        raise SystemExit(f"non-read-only table grants: {grants!r}")

    catalog_sql = """
    SELECT json_build_object(
      'schema', t.table_schema,
      'table', t.table_name,
      'table_type', t.table_type,
      'columns', COALESCE((
        SELECT json_agg(json_build_object(
          'ordinal', c.ordinal_position,
          'name', c.column_name,
          'data_type', c.data_type,
          'udt_name', c.udt_name,
          'nullable', c.is_nullable
        ) ORDER BY c.ordinal_position)
        FROM information_schema.columns c
        WHERE c.table_schema=t.table_schema AND c.table_name=t.table_name
      ), '[]'::json)
    )::text
    FROM information_schema.tables t
    WHERE t.table_schema NOT IN ('pg_catalog','information_schema')
      AND has_table_privilege(current_user, quote_ident(t.table_schema)||'.'||quote_ident(t.table_name), 'SELECT')
    ORDER BY t.table_schema, t.table_name;
    """
    catalog = [json.loads(line) for line in psql(catalog_sql)]
    if not catalog:
        raise SystemExit("empty accessible runtime catalog")

    statements = [
        "BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;",
        "SELECT json_build_object('record_type','snapshot','txid_if_assigned',pg_current_xact_id_if_assigned(),"
        "'timestamp',transaction_timestamp(),"
        "'transaction_read_only',current_setting('transaction_read_only'))::text;",
    ]
    for table in catalog:
        schema = table["schema"]
        name = table["table"]
        qtable = f"{ident(schema)}.{ident(name)}"
        statements.append(
            "SELECT json_build_object('record_type','table_count','schema',"
            f"{literal(schema)},'table',{literal(name)},'row_count',count(*))::text FROM {qtable};"
        )
        for column in table["columns"]:
            col = column["name"]
            if col not in CATEGORICAL_ALLOWLIST:
                continue
            qcol = ident(col)
            statements.append(
                "SELECT json_build_object('record_type','categorical_summary','schema',"
                f"{literal(schema)},'table',{literal(name)},'column',{literal(col)},"
                f"'distinct_count',count(DISTINCT {qcol}),"
                f"'values_emitted',(count(DISTINCT {qcol}) <= 500))::text FROM {qtable};"
            )
            statements.append(
                "SELECT json_build_object('record_type','categorical_value','schema',"
                f"{literal(schema)},'table',{literal(name)},'column',{literal(col)},"
                f"'value',{qcol}::text,'row_count',count(*))::text "
                f"FROM {qtable} WHERE {qcol} IS NOT NULL "
                f"AND (SELECT count(DISTINCT {qcol}) FROM {qtable}) <= 500 "
                f"GROUP BY {qcol} ORDER BY {qcol}::text;"
            )
    statements.append("COMMIT;")
    observation_lines = psql("\n".join(statements))
    observations = [json.loads(line) for line in observation_lines]
    snapshots = [row for row in observations if row.get("record_type") == "snapshot"]
    counts = [row for row in observations if row.get("record_type") == "table_count"]
    categorical_summaries = [row for row in observations if row.get("record_type") == "categorical_summary"]
    categories = [row for row in observations if row.get("record_type") == "categorical_value"]
    if len(snapshots) != 1 or len(counts) != len(catalog):
        raise SystemExit("runtime snapshot did not account for every accessible table")
    if snapshots[0].get("transaction_read_only") != "on":
        raise SystemExit("runtime snapshot was not read-only")

    count_index = {(row["schema"], row["table"]): row["row_count"] for row in counts}
    runtime_units = []
    for ordinal, table in enumerate(catalog, 1):
        key = (table["schema"], table["table"])
        unit_name = f"{key[0]}.{key[1]}"
        runtime_units.append({
            "runtime_unit_id": "RUN-" + sha256(unit_name.encode())[:16].upper(),
            "ordinal": ordinal,
            **table,
            "row_count": count_index[key],
            "final_accounting": "PENDING",
        })

    out.mkdir(parents=True, exist_ok=False)
    def encode_jsonl(rows: list[dict]) -> bytes:
        return b"".join(
            (json.dumps(row, sort_keys=True, separators=(",", ":")) + "\n").encode()
            for row in rows
        )

    units_bytes = encode_jsonl(runtime_units)
    obs_bytes = encode_jsonl(observations)
    categorical_bytes = encode_jsonl(categories)
    (out / "runtime-universe.jsonl").write_bytes(units_bytes)
    (out / "runtime-observations.jsonl").write_bytes(obs_bytes)
    (out / "runtime-categorical-evidence.jsonl").write_bytes(categorical_bytes)
    manifest = {
        "phase": "REFORMULATED_Q3_002_MEASUREMENT_001",
        "question_id": "REFORMULATED-Q3-002",
        "measurement_sha": head,
        "production_mutated": False,
        "query_mode": "single REPEATABLE READ READ ONLY observation transaction after catalog registration",
        "db_identity": identity,
        "table_grants": grants,
        "runtime_unit_count": len(runtime_units),
        "table_count_observation_count": len(counts),
        "categorical_summary_count": len(categorical_summaries),
        "categorical_observation_count": len(categories),
        "runtime_universe_sha256": sha256(units_bytes),
        "runtime_observations_sha256": sha256(obs_bytes),
        "runtime_categorical_evidence_sha256": sha256(categorical_bytes),
        "categorical_allowlist": sorted(CATEGORICAL_ALLOWLIST),
        "model_calls_before_registration": 0,
        "expected_capability_count": None,
    }
    manifest_bytes = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode()
    (out / "runtime-registration-manifest.json").write_bytes(manifest_bytes)
    print(json.dumps({**manifest, "manifest_sha256": sha256(manifest_bytes)}, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
