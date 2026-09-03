package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// EvidenceProofStore is the durable, host-only write/read path for
// DURABLE-EVIDENCE-PROOF-CONTRACT (docs/reports/DURABLE-EVIDENCE-PROOF-CONTRACT.md).
// Every write here is executive.Orchestrator-driven, never worker-driven --
// see evidence_proofs.go's own doc comment on why that is the real
// invariant behind the "minted_by='host'" column, not merely what the
// column happens to say.
type EvidenceProofStore struct{ pool *pgxpool.Pool }

func NewEvidenceProofStore(pool *pgxpool.Pool) (*EvidenceProofStore, error) {
	if pool == nil {
		return nil, errors.New("executive evidence proof store requires a PostgreSQL pool")
	}
	return &EvidenceProofStore{pool: pool}, nil
}

var _ executive.EvidenceProofStore = (*EvidenceProofStore)(nil)

// ValidProofs reads every non-invalidated proof for rootTaskID at baseSHA.
// The WHERE clause naming invalidated_at IS NULL explicitly, rather than
// relying on callers to filter, is deliberate: this is exactly the
// condition whose omission would turn a transport optimization into an
// authority failure (a tombstoned proof silently still counting as valid).
func (s *EvidenceProofStore) ValidProofs(ctx context.Context, rootTaskID int64, baseSHA string) (map[executive.EvidenceSlot]executive.EvidenceProof, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, organization_id, root_task_id, subject, relation, base_sha, source_reference, content_digest
FROM evidence_proofs
WHERE root_task_id = $1 AND base_sha = $2 AND invalidated_at IS NULL`,
		rootTaskID, baseSHA)
	if err != nil {
		return nil, fmt.Errorf("read valid evidence proofs for root %d: %w", rootTaskID, err)
	}
	defer rows.Close()
	out := map[executive.EvidenceSlot]executive.EvidenceProof{}
	for rows.Next() {
		var proof executive.EvidenceProof
		if err := rows.Scan(&proof.ID, &proof.OrganizationID, &proof.RootTaskID, &proof.Subject, &proof.Relation,
			&proof.BaseSHA, &proof.SourceReference, &proof.ContentDigest); err != nil {
			return nil, fmt.Errorf("scan evidence proof: %w", err)
		}
		out[executive.EvidenceSlot{Subject: proof.Subject, Relation: proof.Relation}] = proof
	}
	return out, rows.Err()
}

// MintProof durably records a slot as proven. ON CONFLICT DO NOTHING on the
// canonical-slot unique index: a resumed round that re-derives the same
// admission dry-run must find what the first pass already minted, not
// error on it or mint a redundant second row for the same slot.
func (s *EvidenceProofStore) MintProof(ctx context.Context, proof executive.EvidenceProof) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO evidence_proofs (organization_id, root_task_id, subject, relation, base_sha, source_reference, content_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (root_task_id, subject, relation, base_sha) WHERE invalidated_at IS NULL DO NOTHING`,
		proof.OrganizationID, proof.RootTaskID, proof.Subject, proof.Relation, proof.BaseSHA, proof.SourceReference, proof.ContentDigest)
	if err != nil {
		return fmt.Errorf("mint evidence proof for root %d subject %s: %w", proof.RootTaskID, proof.Subject, err)
	}
	return nil
}

// InvalidateProofs tombstones every valid proof for rootTaskID whose
// base_sha no longer matches currentBaseSHA. Called from the same pass
// that already applies ReasonWorldChangedSinceFreeze, so a proof never
// outlives the freeze it was minted under.
func (s *EvidenceProofStore) InvalidateProofs(ctx context.Context, rootTaskID int64, currentBaseSHA string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE evidence_proofs
SET invalidated_at = NOW()
WHERE root_task_id = $1 AND base_sha <> $2 AND invalidated_at IS NULL`,
		rootTaskID, currentBaseSHA)
	if err != nil {
		return fmt.Errorf("invalidate evidence proofs for root %d: %w", rootTaskID, err)
	}
	return nil
}
