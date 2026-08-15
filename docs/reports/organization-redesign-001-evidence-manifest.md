# ORGANIZATION-REDESIGN-001 — evidence integrity manifest

Cryptographic binding between the durable reports in `docs/reports/` and the
operational evidence that lives outside git.

```
durable report  ->  references off-git artifact  ->  sha256 + byte size
```

**Generated:** 2026-08-15T01:59:03Z
**Host:** explorarte-org · **Campaign directory:** `~/redesign-001`

Raw evidence — DB dumps, physical backups, large logs, model traces — is **not**
copied into git. This manifest binds the reports to those artifacts by hash so that
a reviewer can verify that a file has not changed since the report was written:

```sh
sha256sum ~/redesign-001/state/boundary_repair_report.txt
```

## Required artifacts

| Artifact | Path | Bytes | SHA-256 |
| --- | --- | ---: | --- |
| `boundary_repair_report.txt` | `/home/ubuntu/redesign-001/state/boundary_repair_report.txt` | 4,570 | `bc5456740af5e8b221d76fe5e598de1f0a0b601d16a1ac97fa2b20823fea94a7` |
| `boundary_repair_001.json` | `/home/ubuntu/redesign-001/state/boundary_repair_001.json` | 6,930 | `889e323bde39dd66c38b8ab2f43c920a43808f1170f8b57aa5d71c736ed47f23` |
| `rerun_report.txt` | `/home/ubuntu/redesign-001/state/rerun_report.txt` | 10,141 | `cc03efe7e0185b40f239d25c51f5cdcad51a3e691a38d07b45fb876f2a314490` |
| `campaign_run2_state.json` | `/home/ubuntu/redesign-001/state/campaign_run2_state.json` | 118,893 | `95aab4048131e8eb7f416f257d7095cd149147b7c6d750b5fdc87df091242d50` |
| `q3_body_search.json` | `/home/ubuntu/redesign-001/state/q3_body_search.json` | 13,456 | `97fbdde15de4b421708a8fd634fa2743e9c87d97a753674cbe8711bf23e54008` |

- **`boundary_repair_report.txt`** — BOUNDARY-REPAIR-001 primary report
- **`boundary_repair_001.json`** — structured artifact inventory, hash search results, classifications
- **`rerun_report.txt`** — RERUN-001 mandated FINAL OUTPUT
- **`campaign_run2_state.json`** — RERUN-001 run state: findings, halt history, instrument findings
- **`q3_body_search.json`** — per-entry canonical body search result (63 manifest entries)

## Supporting artifacts

| Artifact | Path | Bytes | SHA-256 |
| --- | --- | ---: | --- |
| `model_calls.jsonl` | `/home/ubuntu/redesign-001/logs/model_calls.jsonl` | 262,339 | `0c033ec1e51d3021d3e555b7c83ba6169a277f8fa73ac7818c355429f634f305` |
| `rerun.log` | `/home/ubuntu/redesign-001/logs/rerun.log` | 35,087 | `e45fe1d7d42f139a9dbaf6d5ea17e2417d42109a0ab522cdef87d886c784bd2d` |
| `deepseek_master_v4.txt` | `/home/ubuntu/redesign-001/prompts/deepseek_master_v4.txt` | 13,949 | `4be9ef43c49f8d5fc8eab75ae556bc5a6593e93f54e1eeb11dce90cfac32b9ff` |
| `test_loop_fix.py` | `/home/ubuntu/redesign-001/_fixtest/test_loop_fix.py` | 23,551 | `64bb5aa025835b4fc1880b8db5a4b0519a669f345760080f5d180f503ea5d912` |
| `campaign_loop_state.json` | `/home/ubuntu/redesign-001/state/campaign_loop_state.json` | 46,388 | `d88342502d0a535f29bea3ebf90a0dd0dfef3907110d0ec6027986db072cd226` |

- **`model_calls.jsonl`** — every model call of the mission; source of all cost figures
- **`rerun.log`** — controller log for RERUN-001 and its resumptions
- **`deepseek_master_v4.txt`** — collection master prompt used by RERUN-001
- **`test_loop_fix.py`** — offline regression harness for the instrument
- **`campaign_loop_state.json`** — campaign 1 state; findings under invalidated_findings

## Missing at manifest time

None. Every referenced artifact existed and was hashed at manifest time.

Convention, for future regenerations: an artifact that is referenced by a durable
report but no longer exists is recorded here as `MISSING_AT_MANIFEST_TIME` and is
**not** recreated — a regenerated file would not be the artifact the report was
written against.

## Scope

This manifest binds evidence provenance only. It makes no claim about the findings
themselves, does not authorize V2, and does not reopen any question.

## Related

- [`organization-redesign-001-boundary-repair-001.md`](organization-redesign-001-boundary-repair-001.md)
- [`organization-redesign-001-rerun-001.md`](organization-redesign-001-rerun-001.md)
