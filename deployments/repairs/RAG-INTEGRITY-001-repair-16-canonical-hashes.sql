-- RAG-INTEGRITY-001 incident repair: correct canonical_hash for the
-- candidates ingested before the AttestedAt-canonicalization fix
-- (PR #29, fix/rag-canonical-timestamp-roundtrip) landed.
--
-- Root cause: Service.Propose() hashed AdmissionAttestation.AttestedAt at
-- whatever precision the caller supplied (here: time.Now().UTC(), a
-- nanosecond-precision value from `orgctl rag ingest-pdf`'s now-removed
-- time.Now() fallback), then persisted it into a Postgres timestamptz
-- column (microsecond precision). Every subsequent read recomputed
-- ComputeCanonicalHash() from the *stored, truncated* value, which never
-- matched the hash computed at write time from the *raw, untruncated*
-- one -- ErrSourceDrift on every read, permanently, since canonical_hash
-- and admission_attested_at are both immutable by design (migration 41).
--
-- This is not a content defect: the persisted admission_attested_at is
-- already the real, correct, microsecond-precision value of when the
-- attestation actually happened. The only wrong value is the hash that
-- was computed before Postgres reduced that precision. This repair
-- corrects exactly that column, in exactly two tables, for exactly the
-- rows affected -- nothing else.
--
-- Of the 16 candidates ingested for the ORGANIZATION-REDESIGN-001 audit
-- corpus, 15 need this repair. One --
-- sakana-fugu-technical-report-v1 -- already recomputes to its own
-- stored hash (its AttestedAt happened to already be a whole multiple of
-- a microsecond) and is correctly excluded from the mapping below; the
-- repair script would refuse to touch it even if it were included, since
-- step 3's precondition check requires the persisted hash to equal the
-- frozen "old" hash, which for that row would not match.
--
-- The 15 corrected hashes were computed by a throwaway, read-only Go
-- program (not committed -- see the incident PR discussion) that: read
-- each row via raw SQL (never repository.Get, which validates and fails
-- exactly on these rows), hydrated a rag.KnowledgeVersion in-process,
-- asserted the persisted admission_attested_at was already UTC and
-- microsecond-safe, and called the real rag.KnowledgeVersion.
-- ComputeCanonicalHash() -- the actual production hashing code, not a
-- reimplementation of its JSON serialization in SQL.
--
-- Scope, deliberately narrow:
--   - Only rag_knowledge_versions.canonical_hash and
--     rag_knowledge_idempotency.canonical_hash are touched.
--   - rag_knowledge_documents, rag_knowledge_evidence_refs,
--     rag_knowledge_lifecycle_events, and rag_knowledge_chunks are never
--     touched and their immutability triggers are never dropped.
--   - The two triggers this script does drop
--     (rag_version_update_guard, rag_knowledge_idempotency_immutable)
--     are dropped and recreated within this same transaction, using
--     their exact current definitions (rag_guard_version_update() as of
--     migration 41; rag_reject_mutation() as of migration 17, unchanged
--     since). Neither guard FUNCTION is ever redefined -- only the two
--     TRIGGERS are momentarily absent, for the duration of this one
--     transaction, and only on these two tables.
--   - No schema migration. No permanent bypass. No change to any other
--     row. Runtime schema tip stays at 48.
--
-- Idempotent to run twice: step 3's precondition check requires the
-- persisted canonical_hash to still equal the frozen "old" value, so a
-- second run finds nothing left to repair and rolls back cleanly at the
-- assertion stage rather than doing anything.

BEGIN;

CREATE TEMP TABLE rag_integrity_001_repair_map (
    version_id       text PRIMARY KEY,
    idempotency_key  text NOT NULL,
    old_canonical_hash text NOT NULL,
    new_canonical_hash text NOT NULL
) ON COMMIT DROP;

INSERT INTO rag_integrity_001_repair_map (version_id, idempotency_key, old_canonical_hash, new_canonical_hash) VALUES
('aixi-monte-carlo-approximation-v1', 'ingest-papers-audit-corpus-2026-08-aixi-monte-carlo-approximation', 'bc7d91d82e8c49485caa9ae2534682d657abfe1a09ac1964de3c5ebfafde6bd6', '9b2eff36365f8ef7ef200014bf87260ce86573a23ad0a6c1ad492ade820832da'),
('bm25-wins-at-scale-rag-paradigms-scaling-study-v1', 'ingest-papers-audit-corpus-2026-08-bm25-wins-at-scale-rag-paradigms-scaling-study', 'b87552b22630bfc7aec6746ca3f16b012a1c747853547ccc0fafaaf59210a511', 'c6bd39ccd07134f9fe3ead5760e8a5d95f169379413de59a946f0ee447b4abc2'),
('conductor-orchestrate-agents-natural-language-v1', 'ingest-papers-audit-corpus-2026-08-conductor-orchestrate-agents-natural-language', '3ab69ec0e3c3a1593a0f64c3749f2ea0570475b7e0157b86b8e8d068b0ee12d7', 'e2c5fe30a2d845bd4fe054638b30a00711fec00e7dfd87ad46a866d1e8042a00'),
('exg-self-evolving-agents-experience-graphs-v1', 'ingest-papers-audit-corpus-2026-08-exg-self-evolving-agents-experience-graphs', 'cf2ded7b0381782c1e71514f62059a42da91c2d08af72377e9d55912274b4420', '4e5a5155110dada901d6b52155b79ad16066af3a2402f24b0d66d0c334e70da4'),
('from-memory-to-skills-coevolution-governance-v1', 'ingest-papers-audit-corpus-2026-08-from-memory-to-skills-coevolution-governance', '3566b8b73a6d477a58d9ef25ff05d960cbda90462fea005d60e43e868f7a8564', '11772bb71bc011d32d5ff865755e1c50f36dbd04cb2aa57352d31eac59000017'),
('managing-procedural-memory-llm-agents-v1', 'ingest-papers-audit-corpus-2026-08-managing-procedural-memory-llm-agents', 'b64cfd53aa3b9af6991f2fc39bfe7a99f2832b99ed7a3b489a7cb6c0ab7efff4', 'da6c5b82d4ee148cee5e9678d7a4de595ffab3de2925204c1e5b5fbd4e576b45'),
('memos-memory-os-ai-system-v1', 'ingest-papers-audit-corpus-2026-08-memos-memory-os-ai-system', '5ca05e1daac8ddb0690796a86d263f31daf9ba50dbe6b52793e1b6d9ef7f6c35', 'd381f75562b873999570e7de918ea0c7c2ec04578be0e1a8e062a6b6f3128540'),
('multimodal-hybrid-rag-scientific-document-understanding-v1', 'ingest-papers-audit-corpus-2026-08-multimodal-hybrid-rag-scientific-document-understanding', '756b4d675f77420a4dbe10f2ba20e6ffacdf842f29d0d5fac3d3181fc2bf8d18', '5a0cf784fd654f5ae25045e7a47228aa1d7edb2b46ab183609df43dc30f023ed'),
('openclaw-skill-collective-skill-tree-search-v1', 'ingest-papers-audit-corpus-2026-08-openclaw-skill-collective-skill-tree-search', 'ec3c14997be7c1d029c905c444e0560fa08da8584321aad3f567c584ad0436bd', 'f7ca764f08977d32014f942401676cc6a67fe7714cd685d9212ab37edc74c96f'),
('planrag-logical-query-trees-exploratory-reasoning-v1', 'ingest-papers-audit-corpus-2026-08-planrag-logical-query-trees-exploratory-reasoning', '43a484480da06117e4db50c156535623216124a63ac5a7b736adcd054e14c158', 'f67b059a189d8d10640434d0bb4ad83a0993ad5c555cfb67b155215205e33ab7'),
('scaling-the-harness-agentic-ai-v1', 'ingest-papers-audit-corpus-2026-08-scaling-the-harness-agentic-ai', '7c74e10251efb5fed3aefec5d12a5bdae962b29da445c5cf05fe43dde0b1a5ab', 'c92cfed37c15b27591653020aca54c4b02b9530510242084452730b081330657'),
('solomonoff-induction-aixi-principles-v1', 'ingest-papers-audit-corpus-2026-08-solomonoff-induction-aixi-principles', 'b45a108fc16469c159d575079b6543dc66eb590ae648b22399ddbd8533726cac', '70a40def7db6b2c7ab259e9a50013228b985ce477fa485d27a832c8b08074db3'),
('trinity-evolved-llm-coordinator-v1', 'ingest-papers-audit-corpus-2026-08-trinity-evolved-llm-coordinator', '1b646517d2b5ba62b860b427461d315e57ef1cca2dddda7bcc3ec7b3fbd74d7f', 'b5aa6e45311c87fa6479074e85e10cf1e615543bacbcdf9b3a7ce8171d7d1d26'),
('vectree-rag-vector-tree-retrieval-v1', 'ingest-papers-audit-corpus-2026-08-vectree-rag-vector-tree-retrieval', '58614182c8ee3a534c9003e9c9866f30ecfe735bb47ad7473527a9006719eac8', 'fb1044ac351fe075e920b4fa38391277cfb6f7a5651ab11f8af06f27e1bb9a87'),
('zenil-solomonoff-induction-limits-self-improving-v1', 'ingest-papers-audit-corpus-2026-08-zenil-solomonoff-induction-limits-self-improving', '86105b2fbcc19cad091187947c6f3aa55539591303e420582f642663ad2cd4fd', '7399853a3da14408b7664295e05977e0d4f19eee7133e827674959ce3c7b027d');

-- Step 1 + 2: lock exactly these rows in both tables before any check.
-- FOR UPDATE cannot combine with an aggregate in the same statement, so
-- the lock and the count are two statements -- the row locks acquired by
-- the first persist for the rest of this transaction regardless.
SELECT 1 FROM rag_knowledge_versions v
    JOIN rag_integrity_001_repair_map m ON m.version_id = v.version_id
    WHERE v.organization_id = 'explorarte'
    FOR UPDATE OF v;

SELECT 1 FROM rag_knowledge_idempotency i
    JOIN rag_integrity_001_repair_map m ON m.idempotency_key = i.idempotency_key
    WHERE i.organization_id = 'explorarte'
    FOR UPDATE OF i;

DO $$
DECLARE locked_count int;
BEGIN
    SELECT count(*) INTO locked_count FROM rag_knowledge_versions v
        JOIN rag_integrity_001_repair_map m ON m.version_id = v.version_id
        WHERE v.organization_id = 'explorarte';
    IF locked_count <> 15 THEN
        RAISE EXCEPTION 'expected to lock 15 version rows, locked %', locked_count;
    END IF;

    SELECT count(*) INTO locked_count FROM rag_knowledge_idempotency i
        JOIN rag_integrity_001_repair_map m ON m.idempotency_key = i.idempotency_key
        WHERE i.organization_id = 'explorarte';
    IF locked_count <> 15 THEN
        RAISE EXCEPTION 'expected to lock 15 idempotency rows, locked %', locked_count;
    END IF;
END $$;

-- Step 3: full precondition check. Any mismatch raises and the whole
-- transaction rolls back -- nothing past this point runs unless every
-- one of these holds for every one of the 15 rows.
DO $$
DECLARE bad_count int;
BEGIN
    SELECT count(*) INTO bad_count FROM rag_integrity_001_repair_map m
        LEFT JOIN rag_knowledge_versions v ON v.organization_id = 'explorarte' AND v.version_id = m.version_id
        WHERE v.version_id IS NULL;
    IF bad_count <> 0 THEN RAISE EXCEPTION 'missing % target version rows', bad_count; END IF;

    SELECT count(*) INTO bad_count FROM rag_integrity_001_repair_map m
        LEFT JOIN rag_knowledge_idempotency i ON i.organization_id = 'explorarte' AND i.idempotency_key = m.idempotency_key
        WHERE i.idempotency_key IS NULL;
    IF bad_count <> 0 THEN RAISE EXCEPTION 'missing % target idempotency rows', bad_count; END IF;

    SELECT count(*) INTO bad_count FROM rag_knowledge_versions v
        JOIN rag_integrity_001_repair_map m ON m.version_id = v.version_id
        WHERE v.organization_id <> 'explorarte'
           OR v.lifecycle <> 'candidate'
           OR v.revision <> 1
           OR v.reviewer_role_id IS NOT NULL
           OR v.reviewed_at IS NOT NULL
           OR v.canonical_hash <> m.old_canonical_hash;
    -- admission_attested_at's precision is not checked here: timestamptz
    -- physically cannot store sub-microsecond precision, so any value
    -- already persisted in this column is microsecond-safe by
    -- construction -- there is nothing a SQL predicate could catch that
    -- the column type itself does not already guarantee.
    IF bad_count <> 0 THEN RAISE EXCEPTION 'precondition violated for % version rows', bad_count; END IF;

    SELECT count(*) INTO bad_count FROM rag_knowledge_versions v
        JOIN rag_integrity_001_repair_map m ON m.version_id = v.version_id
        WHERE EXISTS (SELECT 1 FROM rag_knowledge_chunks c WHERE c.organization_id = v.organization_id AND c.version_id = v.version_id);
    IF bad_count <> 0 THEN RAISE EXCEPTION 'chunks present for % rows -- these should still be unreviewed candidates', bad_count; END IF;

    SELECT count(*) INTO bad_count FROM rag_knowledge_idempotency i
        JOIN rag_integrity_001_repair_map m ON m.idempotency_key = i.idempotency_key
        WHERE i.organization_id <> 'explorarte'
           OR i.version_id <> m.version_id
           OR i.canonical_hash <> m.old_canonical_hash;
    IF bad_count <> 0 THEN RAISE EXCEPTION 'precondition violated for % idempotency rows', bad_count; END IF;

    -- Corrected hashes must not collide with ANY existing canonical_hash
    -- in the table (the unique constraint would catch this too, but
    -- fail with a clear message before attempting the UPDATE, not after).
    SELECT count(*) INTO bad_count FROM rag_integrity_001_repair_map m
        WHERE EXISTS (SELECT 1 FROM rag_knowledge_versions v2 WHERE v2.organization_id = 'explorarte' AND v2.canonical_hash = m.new_canonical_hash);
    IF bad_count <> 0 THEN RAISE EXCEPTION '% corrected hashes collide with an existing canonical_hash', bad_count; END IF;
END $$;

-- Step 4 + 5: drop exactly the two triggers and the one FK this repair
-- needs to move through. Every other guard on every other table stays
-- untouched and enabled for the entire transaction.
DROP TRIGGER rag_version_update_guard ON rag_knowledge_versions;
DROP TRIGGER rag_knowledge_idempotency_immutable ON rag_knowledge_idempotency;
ALTER TABLE rag_knowledge_idempotency DROP CONSTRAINT rag_knowledge_idempotency_version_fk;

-- Step 6 + 7: correct the version rows (only canonical_hash changes --
-- not updated_at, not revision, not lifecycle, not anything else), then
-- assert exactly 15 rows were touched, in the same PL/pgSQL block so
-- GET DIAGNOSTICS actually sees this UPDATE's row count.
DO $$
DECLARE updated_count int;
BEGIN
    UPDATE rag_knowledge_versions v
    SET canonical_hash = m.new_canonical_hash
    FROM rag_integrity_001_repair_map m
    WHERE v.organization_id = 'explorarte' AND v.version_id = m.version_id AND v.canonical_hash = m.old_canonical_hash;
    GET DIAGNOSTICS updated_count = ROW_COUNT;
    IF updated_count <> 15 THEN RAISE EXCEPTION 'expected to update 15 version rows, updated %', updated_count; END IF;
END $$;

-- Step 8 + 9: correct the matching idempotency rows, same pattern.
DO $$
DECLARE updated_count int;
BEGIN
    UPDATE rag_knowledge_idempotency i
    SET canonical_hash = m.new_canonical_hash
    FROM rag_integrity_001_repair_map m
    WHERE i.organization_id = 'explorarte' AND i.idempotency_key = m.idempotency_key AND i.canonical_hash = m.old_canonical_hash;
    GET DIAGNOSTICS updated_count = ROW_COUNT;
    IF updated_count <> 15 THEN RAISE EXCEPTION 'expected to update 15 idempotency rows, updated %', updated_count; END IF;
END $$;

-- Step 10: recreate the FK exactly as canonical schema (migration 17) defines it.
ALTER TABLE rag_knowledge_idempotency
    ADD CONSTRAINT rag_knowledge_idempotency_version_fk
    FOREIGN KEY (organization_id, version_id, canonical_hash)
    REFERENCES rag_knowledge_versions (organization_id, version_id, canonical_hash) ON DELETE RESTRICT;

-- Step 11: recreate the two triggers, same names, same guard functions
-- (rag_guard_version_update as of migration 41; rag_reject_mutation as
-- of migration 17) -- neither function is redefined by this script.
CREATE TRIGGER rag_version_update_guard BEFORE UPDATE ON rag_knowledge_versions FOR EACH ROW EXECUTE FUNCTION rag_guard_version_update();
CREATE TRIGGER rag_knowledge_idempotency_immutable BEFORE UPDATE OR DELETE ON rag_knowledge_idempotency FOR EACH ROW EXECUTE FUNCTION rag_reject_mutation();

-- Step 12: postconditions.
DO $$
DECLARE v_count int; i_count int; old_left int; new_count int; fk_valid boolean; trig_count int;
BEGIN
    SELECT count(*) INTO v_count FROM rag_knowledge_versions WHERE namespace_id = 'investigacion';
    IF v_count <> 16 THEN RAISE EXCEPTION 'expected 16 versions to remain in investigacion, found %', v_count; END IF;

    SELECT count(*) INTO i_count FROM rag_knowledge_idempotency i JOIN rag_knowledge_versions v ON v.organization_id = i.organization_id AND v.version_id = i.version_id WHERE v.namespace_id = 'investigacion';
    IF i_count <> 16 THEN RAISE EXCEPTION 'expected 16 idempotency records to remain, found %', i_count; END IF;

    SELECT count(*) INTO old_left FROM rag_knowledge_versions v JOIN rag_integrity_001_repair_map m ON m.version_id = v.version_id WHERE v.canonical_hash = m.old_canonical_hash;
    IF old_left <> 0 THEN RAISE EXCEPTION '% rows still carry an old canonical_hash', old_left; END IF;

    SELECT count(*) INTO new_count FROM rag_knowledge_versions v JOIN rag_integrity_001_repair_map m ON m.version_id = v.version_id WHERE v.canonical_hash = m.new_canonical_hash;
    IF new_count <> 15 THEN RAISE EXCEPTION 'expected 15 rows to carry the corrected canonical_hash, found %', new_count; END IF;

    SELECT count(*) INTO trig_count FROM pg_trigger WHERE tgname IN ('rag_version_update_guard', 'rag_knowledge_idempotency_immutable', 'rag_versions_no_delete') AND NOT tgisinternal;
    IF trig_count <> 3 THEN RAISE EXCEPTION 'expected 3 immutability triggers present, found %', trig_count; END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'rag_knowledge_idempotency_version_fk') THEN
        RAISE EXCEPTION 'rag_knowledge_idempotency_version_fk missing after repair';
    END IF;
END $$;

COMMIT;
