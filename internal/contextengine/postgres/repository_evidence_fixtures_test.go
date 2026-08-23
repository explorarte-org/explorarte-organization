//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// segmentRow is a context segment as the DATABASE sees it: the classification
// fields are plain strings so a test can write a combination the Go domain
// would never construct. That is the point -- the constraint has to hold for
// writers that do not go through the assembler.
type segmentRow struct {
	kind        string
	reference   string
	version     string
	instruction string
	trust       string
	data        string
	mayGrant    bool
	content     string
}

var segmentFixtureSeq atomic.Int64

func digestFor(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// insertSnapshotForTest creates the minimum durable snapshot a segment can
// belong to. It uses the organization and role the surrounding integration
// fixtures already sync, so the foreign keys resolve.
func insertSnapshotForTest(ctx context.Context, platform *platformpostgres.Store) (int64, error) {
	seq := segmentFixtureSeq.Add(1)
	key := fmt.Sprintf("repository-evidence-fixture-%d", seq)
	var revisionID int64
	if err := platform.Pool().QueryRow(ctx,
		`SELECT id FROM organization_registry_revisions ORDER BY id DESC LIMIT 1`).Scan(&revisionID); err != nil {
		return 0, fmt.Errorf("no registry revision to attach the snapshot to: %w", err)
	}
	var roleID string
	if err := platform.Pool().QueryRow(ctx,
		`SELECT id FROM organization_roles WHERE organization_id=$1 ORDER BY id LIMIT 1`,
		integrationOrganization).Scan(&roleID); err != nil {
		return 0, fmt.Errorf("no role to attribute the snapshot to: %w", err)
	}
	var snapshotID int64
	err := platform.Pool().QueryRow(ctx, `
		INSERT INTO context_snapshots (
			organization_id, organization_revision_id, actor_role_id, purpose,
			idempotency_key, request_hash, precedence_hash, canonical_bundle_hash,
			rendered_hash, status, segment_count, included_segment_count,
			omitted_segment_count, total_bytes, created_at)
		VALUES ($1,$2,$3,'repository evidence persistence test',$4,$5,$5,$5,$5,'ready',1,1,0,0,$6)
		RETURNING id`,
		integrationOrganization, revisionID, roleID, key, digestFor(key), time.Now().UTC()).Scan(&snapshotID)
	return snapshotID, err
}

// tryInsertSegment writes one segment straight to PostgreSQL and returns
// whatever the database says. No Go-side validation stands between the row
// and the constraint.
func tryInsertSegment(ctx context.Context, platform *platformpostgres.Store, row segmentRow) (int64, error) {
	snapshotID, err := insertSnapshotForTest(ctx, platform)
	if err != nil {
		return 0, err
	}
	content := []byte(row.content)
	_, err = platform.Pool().Exec(ctx, `
		INSERT INTO context_segments (
			snapshot_id, organization_id, ordinal, render_ordinal,
			authority_priority, authority_tier, source_kind, source_reference,
			source_version, instruction_class, trust_class, data_class,
			may_grant_capabilities, included, content_hash, byte_count, content, created_at)
		VALUES ($1,$2,1,1,6,'rag_evidence',$3,$4,$5,$6,$7,$8,$9,TRUE,$10,$11,$12,$13)`,
		snapshotID, integrationOrganization, row.kind, row.reference, row.version,
		row.instruction, row.trust, row.data, row.mayGrant,
		digestFor(row.content), len(content), content, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return snapshotID, nil
}

func insertSegmentForTest(t *testing.T, ctx context.Context, platform *platformpostgres.Store, row segmentRow) int64 {
	t.Helper()
	snapshotID, err := tryInsertSegment(ctx, platform, row)
	if err != nil {
		t.Fatalf("a well-formed repository evidence segment was refused: %v", err)
	}
	return snapshotID
}
