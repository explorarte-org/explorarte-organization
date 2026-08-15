# ORGANIZATION-REDESIGN-001 — evidence integrity manifest

Cryptographic binding between the durable reports in `docs/reports/` and the
operational evidence that lives outside git.

```
durable report  ->  references off-git artifact  ->  sha256 + byte size
```

**Generated:** 2026-08-15T01:59:03Z
**Host:** explorarte-org · **Campaign directory:** `~/redesign-001`

Raw evidence — DB dumps, physical backups, large logs, model traces — is **not**
copied into git. Each hash binds the artifact **as observed at manifest time**.
Direct re-verification from the recorded path is possible only while that exact byte
stream remains retained there, or at another recorded preservation path. Several of
these paths are mutable — append-only logs and evolving test files — so a historical
hash is a record of what was observed, not a guarantee that the same bytes are still
retrievable from that path. See *Mutable-path provenance* below.

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


## RERUN-002 artifacts

Added 2026-08-15T13:52:13Z. Same rule: hashes bind the durable reports to off-git evidence; no raw artifact is copied into git.

| Artifact | Path | Bytes | SHA-256 | Status |
| --- | --- | ---: | --- | --- |
| `rerun002_report.txt` | `/home/ubuntu/redesign-001/state/rerun002_report.txt` | 8,645 | `948098abaff74c7e172870428b13e566386da1d9272460d97fa8a4ed80dda340` | `PRESENT_AT_MANIFEST_TIME` |
| `campaign_run3_state.json` | `/home/ubuntu/redesign-001/state/campaign_run3_state.json` | 114,245 | `b68bc4760917c8af541e6720685fd6647ebf27bb61cdd9e10415de28ba8b426b` | `PRESENT_AT_MANIFEST_TIME` |
| `root_mismatch_replay.json` | `/home/ubuntu/redesign-001/state/root_mismatch_replay.json` | 9,987 | `816ee29a853de0fac31ab1127646130b89d89924f6127112efc9256dc3133286` | `PRESENT_AT_MANIFEST_TIME` |
| `reliability_replay.json` | `/home/ubuntu/redesign-001/state/reliability_replay.json` | 1,066 | `971d0dfef9e46bf706ce5c592605b25fedee23a7779a0731ee3f05bd50967597` | `PRESENT_AT_MANIFEST_TIME` |
| `q2_equivalence.json` | `/home/ubuntu/redesign-001/state/q2_equivalence.json` | 5,878 | `0eed6453461069cfd1947e628ba90b7b3aae7b9ee4eac02c843a4cd7dd658468` | `PRESENT_AT_MANIFEST_TIME` |
| `transport_attempts.jsonl` | `/home/ubuntu/redesign-001/logs/transport_attempts.jsonl` | 46,451 | `c6e6777356bb0f9479ecdbd1b98f31dd3eb64557e35a4e2a71edd179b4544c2c` | `PRESENT_AT_MANIFEST_TIME` |
| `tool_trajectory.jsonl` | `/home/ubuntu/redesign-001/logs/tool_trajectory.jsonl` | 340,280 | `fa527878dbb9a0ca6d869b02313f6e2c33500c65bbb6f93b61f95bb2e925f1dc` | `PRESENT_AT_MANIFEST_TIME` |
| `rerun002.log` | `/home/ubuntu/redesign-001/logs/rerun002.log` | 37,400 | `b56d2f3f9ba3d5a454b461840a2aac591b61cff26a1f6958c675fcee1098f72b` | `PRESENT_AT_MANIFEST_TIME` |
| `test_loop_fix.py` | `/home/ubuntu/redesign-001/_fixtest/test_loop_fix.py` | 35,191 | `18e567df3cfdf6cb7ca0934f49665a69c1130650b4015e64f7570b8a7a52d50e` | `PRESENT_AT_MANIFEST_TIME` |
| `proc_identity.py` | `/home/ubuntu/redesign-001/proc_identity.py` | 1,538 | `12217c24805287252f92ffa59f6542c382030343a2c2cf3154b16866434c859a` | `PRESENT_AT_MANIFEST_TIME` |
| `model_calls.jsonl` | `/home/ubuntu/redesign-001/logs/model_calls.jsonl` | 465,210 | `e52f1ae59bafb1a50ea86a3debf50051a6a7f07e1e5215c171176ed4491b1221` | `PRESENT_AT_MANIFEST_TIME` |
| `gate_replay.json` | `/home/ubuntu/redesign-001/state/gate_replay.json` | 2,853 | `976c6a11d40d2f21a9137d7e6b47ae12d1426f6982b961e75ba848667f72d588` | `PRESENT_AT_MANIFEST_TIME` |
| `q3_body_search.json` | `/home/ubuntu/redesign-001/state/q3_body_search.json` | 13,456 | `97fbdde15de4b421708a8fd634fa2743e9c87d97a753674cbe8711bf23e54008` | `PRESENT_AT_MANIFEST_TIME` |
| `campaign_run3_state.json.bak-before-taskA` | `/home/ubuntu/redesign-001/state/campaign_run3_state.json.bak-before-taskA` | 100,285 | `00e99b4ab7bf3cde4113a37902a99ccc1dbc0e9eeb6f9d37ed1ab2d77aa06a5d` | `PRESENT_AT_MANIFEST_TIME` |
| `mutable_path_provenance.json` | `/home/ubuntu/redesign-001/state/mutable_path_provenance.json` | 1,072 | `ce3e59b1904abf83a78586f9f5bb41bfb145d27f8aa042056feb36e147b6a235` | `PRESENT_AT_MANIFEST_TIME` |

- **`rerun002_report.txt`** — RERUN-002 mandated FINAL OUTPUT (CLEAN_BASELINE_VALID)
- **`campaign_run3_state.json`** — RERUN-002 run state: findings, rulings, aborted runs, context evidence registry
- **`root_mismatch_replay.json`** — per-call replay of RERUN-001 against both trees (root mismatch impact)
- **`reliability_replay.json`** — Q4 extractor replay result and Q2 resume point
- **`q2_equivalence.json`** — Q2_RESUME_EQUIVALENCE_CHECK: task A vs reconciler task B
- **`transport_attempts.jsonl`** — every transport attempt with request hash, result and bytes
- **`tool_trajectory.jsonl`** — every tool call with resolved measurement root and result hash
- **`rerun002.log`** — controller log for RERUN-002
- **`test_loop_fix.py`** — offline regression harness at 141 checks
- **`proc_identity.py`** — exact process identity used instead of fuzzy pgrep
- **`model_calls.jsonl`** — every model call of the mission; source of all cost figures
- **`gate_replay.json`** — citation-gate replay of the stored Q2/Q4 packets
- **`q3_body_search.json`** — canonical body search (63 manifest entries) from BOUNDARY-REPAIR-001
- **`campaign_run3_state.json.bak-before-taskA`** — Q2 pending-A preservation: state immediately before task A was restored
- **`mutable_path_provenance.json`** — result of the deterministic mutable-path search below; it now carries a provenance claim, so it is bound here like any other evidence artifact

### Mutable-path provenance

Two recorded paths are mutable and have been hashed twice by this manifest, at different
times, with different content. Recording both without comment would imply the earlier hash
still verifies at that path. It does not.

| Path | Historical record | Current record | Historical bytes still at that path? |
| --- | --- | --- | --- |
| `~/redesign-001/logs/model_calls.jsonl` | `0c033ec1e51d3021…` · 262,339 B | `e52f1ae59bafb1a5…` · 465,210 B | not as a standalone file — retained as a verified prefix |
| `~/redesign-001/_fixtest/test_loop_fix.py` | `64bb5aa025835b4f…` · 23,551 B | `18e567df3cfdf6cb…` · 35,191 B | no — file was edited in place |

Both historical hashes were valid at their manifest-generation time. The same paths were
subsequently extended or replaced and now bind the RERUN-002 hashes. **A historical hash is
not evidence that the exact historical byte stream remains retrievable from that mutable
path.**

A deterministic search over `/home/ubuntu`, `/opt/explorarte`, `/srv` and `/tmp`
(93,260 files inspected, matched by size then by full sha256) found **no exact standalone copy**
of either historical byte stream. Neither was recreated.

The two cases are not equivalent, and are recorded separately:

**`model_calls.jsonl`**
```
HISTORICAL_BYTES_RETAINED_AS_VERIFIED_PREFIX
EXACT_STANDALONE_ARTIFACT_NOT_RETAINED
```
The log is append-only. The first 262,339 bytes of the current file hash to
`0c033ec1e51d3021…` exactly — the historical byte stream survives, verifiably, as a prefix of
the current file. What no longer exists is a standalone artifact at that path whose whole
content hashes to the historical value. A reviewer can re-verify the historical record by
hashing the first 262,339 bytes.

**`test_loop_fix.py`**
```
HISTORICAL_HASH_ONLY
EXACT_BYTES_NOT_CURRENTLY_RETAINED
```
The file was edited in place, not appended. Its prefix does not match the historical hash and
no copy of the historical bytes was found anywhere in the searched roots. Only the hash record
survives.

Result data: `~/redesign-001/state/mutable_path_provenance.json`.

## Scope

This manifest binds evidence provenance only. It makes no claim about the findings
themselves, does not authorize V2, and does not reopen any question.

## Related

- [`organization-redesign-001-boundary-repair-001.md`](organization-redesign-001-boundary-repair-001.md)
- [`organization-redesign-001-rerun-001.md`](organization-redesign-001-rerun-001.md)
- [`organization-redesign-001-rerun-002.md`](organization-redesign-001-rerun-002.md)
