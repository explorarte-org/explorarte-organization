//go:build integration

package ceochat_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5/pgconn"
)

// CAMPAIGN_GOVERNED_EXECUTION_MODE_V1 against REAL PostgreSQL, the real Task
// Engine and the real Executive: the mode an owner chose is written once, as
// durable root requirements plus a promotion row, and nothing afterwards can
// move it.

func (o *ownerPromotionFixture) rootRequirementKeys(t *testing.T, rootID int64) []string {
	t.Helper()
	rows, err := o.store.Pool().Query(context.Background(), `SELECT requirement_key FROM task_requirements WHERE task_id=$1`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (o *ownerPromotionFixture) promotionMode(t *testing.T, promotionID int64) (mode string, approvalID int64) {
	t.Helper()
	if err := o.store.Pool().QueryRow(context.Background(), `SELECT execution_mode, owner_approval_id FROM campaign_promotions WHERE id=$1`, promotionID).Scan(&mode, &approvalID); err != nil {
		t.Fatal(err)
	}
	return mode, approvalID
}

var analysisOnlyRootKeys = []string{"executive_closure_verified"}

func governedRootKeys() []string {
	keys := []string{"executive_closure_verified", executive.MissionRequirementKey, executive.CodeRunnerExecutionEvidenceRequirementKey, "design-freeze"}
	sort.Strings(keys)
	return keys
}

// Absence of a mode is today's behavior, on the real stack: an analysis_only
// promotion whose root carries no governed requirement, recorded as such.
func TestRealStackPromotionWithoutAModeIsAnalysisOnly(t *testing.T) {
	o := newOwnerPromotionFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	result, err := o.promoter(t).Promote(context.Background(), o.approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := o.rootRequirementKeys(t, result.ExecutiveRootTaskID); !reflect.DeepEqual(got, analysisOnlyRootKeys) {
		t.Fatalf("root requirements = %v, want %v", got, analysisOnlyRootKeys)
	}
	if mode, approval := o.promotionMode(t, result.PromotionID); mode != "analysis_only" || approval != o.approval.ID {
		t.Fatalf("promotion row = (%q, %d)", mode, approval)
	}
}

// A valid owner choice yields exactly the canonical bundle, tied durably to the
// owner approval and the promotion; retries and a contrary request cannot move it.
func TestRealStackGovernedModeWritesTheCanonicalBundleAndIsImmutable(t *testing.T) {
	o := newOwnerPromotionFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	ctx := context.Background()

	result, err := o.promoter(t).PromoteWithMode(ctx, o.approval.ID, executive.ExecutionModeGovernedImplementation)
	if err != nil {
		t.Fatalf("governed promotion: %v", err)
	}
	want := governedRootKeys()
	if got := o.rootRequirementKeys(t, result.ExecutiveRootTaskID); !reflect.DeepEqual(got, want) {
		t.Fatalf("root requirements = %v, want exactly %v", got, want)
	}
	if mode, approval := o.promotionMode(t, result.PromotionID); mode != "governed_implementation" || approval != o.approval.ID || result.ExecutionMode != executive.ExecutionModeGovernedImplementation {
		t.Fatalf("provenance = (%q, approval %d), result mode %q", mode, approval, result.ExecutionMode)
	}

	// Same choice again: converges.
	again, err := o.promoter(t).PromoteWithMode(ctx, o.approval.ID, executive.ExecutionModeGovernedImplementation)
	if err != nil || !again.Reused || again.PromotionID != result.PromotionID || again.ExecutiveRootTaskID != result.ExecutiveRootTaskID {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	// A contrary explicit choice is refused, and the CEO-tool-shaped path reads the
	// governed promotion without touching it.
	if _, err := o.promoter(t).PromoteWithMode(ctx, o.approval.ID, executive.ExecutionModeAnalysisOnly); !errors.Is(err, campaign.ErrExecutionModeConflict) {
		t.Fatalf("contrary mode: err = %v, want ErrExecutionModeConflict", err)
	}
	viaPlain, err := o.promoter(t).Promote(ctx, o.approval.ID)
	if err != nil || !viaPlain.Reused || viaPlain.ExecutionMode != executive.ExecutionModeGovernedImplementation {
		t.Fatalf("mode-less re-read = %+v, %v; want the governed promotion reported as it is", viaPlain, err)
	}
	if roots, promotions := o.rootsAndPromotions(t); roots != 1 || promotions != 1 {
		t.Fatalf("roots %d, promotions %d; want 1, 1", roots, promotions)
	}
	if got := o.rootRequirementKeys(t, result.ExecutiveRootTaskID); !reflect.DeepEqual(got, want) {
		t.Fatalf("requirements moved to %v after retries", got)
	}
}

// A root created under one mode whose promotion row was never written (a crash)
// is NOT adopted by a retry that asks for the other mode: the Task Engine's
// request hash covers the requirements, so it is an idempotency conflict, and no
// promotion is recorded under a mode the root does not have.
func TestRealStackRetryUnderAnotherModeCannotAdoptTheExistingRoot(t *testing.T) {
	o := newOwnerPromotionFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	ctx := context.Background()

	first, err := o.promoter(t).Promote(ctx, o.approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.store.Pool().Exec(ctx, `DELETE FROM campaign_promotions WHERE id=$1`, first.PromotionID); err != nil {
		t.Fatalf("simulate the crash before the promotion row: %v", err)
	}
	_, err = o.promoter(t).PromoteWithMode(ctx, o.approval.ID, executive.ExecutionModeGovernedImplementation)
	if !errors.Is(err, tasks.ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want tasks.ErrIdempotencyConflict", err)
	}
	if roots, promotions := o.rootsAndPromotions(t); roots != 1 || promotions != 0 {
		t.Fatalf("roots %d, promotions %d; want the one historical root and no promotion", roots, promotions)
	}
	if got := o.rootRequirementKeys(t, first.ExecutiveRootTaskID); !reflect.DeepEqual(got, analysisOnlyRootKeys) {
		t.Fatalf("the historical root's requirements became %v", got)
	}
}

// Fails before any work exists: an approval that is not there.
func TestRealStackGovernedModeWithoutAValidApprovalCreatesNoWork(t *testing.T) {
	o := newOwnerPromotionFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	_, err := o.promoter(t).PromoteWithMode(context.Background(), o.approval.ID+1000, executive.ExecutionModeGovernedImplementation)
	if !errors.Is(err, campaign.ErrApprovalNotFound) {
		t.Fatalf("err = %v, want ErrApprovalNotFound", err)
	}
	if roots, promotions := o.rootsAndPromotions(t); roots != 0 || promotions != 0 {
		t.Fatalf("roots %d, promotions %d; want none", roots, promotions)
	}
}

// Migration 000080 as the database actually has it: the column is NOT NULL,
// defaults to analysis_only (so every promotion that predates the mode reads as
// what it was), and refuses any value that is not one of the two known modes.
func TestRealStackExecutionModeColumnContract(t *testing.T) {
	o := newOwnerPromotionFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	ctx := context.Background()

	var nullable, def string
	if err := o.store.Pool().QueryRow(ctx, `SELECT is_nullable, COALESCE(column_default, '') FROM information_schema.columns
		WHERE table_schema='public' AND table_name='campaign_promotions' AND column_name='execution_mode'`).Scan(&nullable, &def); err != nil {
		t.Fatalf("execution_mode column: %v", err)
	}
	if nullable != "NO" || !strings.Contains(def, "analysis_only") {
		t.Fatalf("execution_mode is_nullable=%q default=%q, want NOT NULL DEFAULT 'analysis_only'", nullable, def)
	}

	result, err := o.promoter(t).Promote(ctx, o.approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"governed", "GOVERNED_IMPLEMENTATION", "design-freeze", ""} {
		_, err := o.store.Pool().Exec(ctx, `UPDATE campaign_promotions SET execution_mode=$2 WHERE id=$1`, result.PromotionID, bad)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("execution_mode=%q: err = %v, want a check violation (23514)", bad, err)
		}
	}
	if mode, _ := o.promotionMode(t, result.PromotionID); mode != "analysis_only" {
		t.Fatalf("mode = %q after rejected updates", mode)
	}
}
