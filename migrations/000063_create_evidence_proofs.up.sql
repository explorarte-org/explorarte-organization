-- DURABLE-EVIDENCE-PROOF-CONTRACT (docs/reports/DURABLE-EVIDENCE-PROOF-CONTRACT.md).
--
-- Fixes EVIDENCE_CAPACITY_LIVENESS_INCOMPLETENESS
-- (docs/reports/CAPACITY-LIVENESS-INVESTIGATION.md): probeAdjudicationRequirements
-- re-pays the full raw-evidence cost of every historically-in-force obligation
-- on every round, against a fixed per-snapshot capacity, while obligations
-- themselves accumulate monotonically and are never discharged. A mission
-- with enough legitimately-necessary subjects eventually has no admissible
-- snapshot, even though every constituent fact is true -- reproduced live,
-- not hypothetical (SELF-AUDIT-001, root task 18862, 2026-09-03).
--
-- A proof does not relax obligation monotonicity (still never discharged or
-- removed) or provenance strictness (still traces to real, host-classified
-- content). It separates two things this system currently conflates: an
-- obligation staying in force, and its raw evidence needing to be
-- re-transported every round. Once a (subject, relation) is durably proven
-- against a base_sha, a later round's joint-capacity probe can treat it as
-- already delivered without re-spending MaxRanges/MaxBytes/MaxLines on it.
CREATE TABLE evidence_proofs (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id  TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    root_task_id     BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    subject          TEXT NOT NULL CHECK (length(trim(subject)) BETWEEN 1 AND 200),
    -- Matches the only two relations ExcerptRelations ever classifies
    -- (internal/repositoryevidence/classify.go) -- a proof cannot name a
    -- relation the classifier could never have produced.
    relation         TEXT NOT NULL CHECK (relation IN ('definition', 'application')),
    base_sha         TEXT NOT NULL CHECK (base_sha ~ '^[0-9a-f]{40}$'),
    -- The exact repository:// citation this proof was minted from --
    -- traceability requires origin identity, not only content integrity
    -- (content_digest alone would prove WHAT was read, not WHERE from).
    source_reference TEXT NOT NULL CHECK (length(trim(source_reference)) BETWEEN 1 AND 2048),
    content_digest   TEXT NOT NULL CHECK (content_digest ~ '^[0-9a-f]{64}$'),
    -- Not merely documentation: minted_by is a literal, checkable column so
    -- the invariant "a model can never supply an object that is persisted
    -- directly as a proof" is enforceable in SQL, not only true by
    -- convention of which Go code happens to call the insert.
    minted_by        TEXT NOT NULL DEFAULT 'host' CHECK (minted_by = 'host'),
    -- The one field this table ever sets after insert (see the immutability
    -- trigger below): tombstoned by the same pass that already invalidates
    -- a frozen design via ReasonWorldChangedSinceFreeze
    -- (internal/executive/design_freeze_phase.go) when base_sha's promotion
    -- target moves. A retired proof stays readable as a retired proof --
    -- provenance requires knowing one existed, not silently vanishing it.
    invalidated_at   TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- v1 invariant, explicit and deliberate (not yet a design gap): exactly one
-- canonical, currently-valid proof per (root_task_id, subject, relation,
-- base_sha). If a future need arises for multiple independent evidence sets
-- to satisfy one relation within the same SHA, this uniqueness is the first
-- thing to revisit -- not silently work around.
CREATE UNIQUE INDEX evidence_proofs_canonical_slot
    ON evidence_proofs (root_task_id, subject, relation, base_sha)
    WHERE invalidated_at IS NULL;

CREATE INDEX evidence_proofs_root_task_idx ON evidence_proofs (root_task_id);

CREATE OR REPLACE FUNCTION guard_evidence_proof_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evidence proofs are immutable: delete is never permitted';
    END IF;
    -- TG_OP = 'UPDATE'. The only permitted transition is invalidating a
    -- still-valid proof, and nothing else may change in the same statement
    -- -- this is what keeps "invalidate on WORLD_CHANGED" from becoming a
    -- side channel for silently rewriting what a proof actually attests.
    IF OLD.invalidated_at IS NOT NULL THEN
        RAISE EXCEPTION 'evidence proofs are immutable: already invalidated';
    END IF;
    IF NEW.invalidated_at IS NULL THEN
        RAISE EXCEPTION 'evidence proofs are immutable: only invalidation is permitted';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.organization_id IS DISTINCT FROM OLD.organization_id
        OR NEW.root_task_id IS DISTINCT FROM OLD.root_task_id
        OR NEW.subject IS DISTINCT FROM OLD.subject
        OR NEW.relation IS DISTINCT FROM OLD.relation
        OR NEW.base_sha IS DISTINCT FROM OLD.base_sha
        OR NEW.source_reference IS DISTINCT FROM OLD.source_reference
        OR NEW.content_digest IS DISTINCT FROM OLD.content_digest
        OR NEW.minted_by IS DISTINCT FROM OLD.minted_by
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'evidence proofs are immutable: only invalidated_at may be set';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER evidence_proofs_guard
BEFORE UPDATE OR DELETE ON evidence_proofs
FOR EACH ROW EXECUTE FUNCTION guard_evidence_proof_mutation();
