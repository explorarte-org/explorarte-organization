package costledgerfixtures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	"github.com/Mireuz13/explorarte-organization/internal/evaluationdb"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

const (
	fixtureOrganization = "explorarte"
	fixtureActor        = "ingenieria_ia/orquestador"
)

// Runner proves orphan visibility and wallet idempotency through the real
// append-only costledger. It never settles the deliberately orphaned row.
type Runner struct{ Store *platformpostgres.Store }

var _ fixtures.Runner = Runner{}

func (Runner) Supports(f fixtures.Fixture) bool {
	return f.ID == fixtureOrphanedReservation && f.RunnerKind == "costledger" && f.Status == fixtures.StatusRunnerReady
}

func (r Runner) Run(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	if err := evaluationdb.RequireDisposable(ctx, r.Store); err != nil {
		return fixtures.RunOutcome{}, err
	}
	scenario, ok := f.Scenario.(*Scenario)
	if !ok || scenario.FixtureID != f.ID {
		return fixtures.RunOutcome{}, fmt.Errorf("fixture %s was not activated by costledgerfixtures.Activate", f.ID)
	}
	ledger, err := costledgerpostgres.New(r.Store)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}

	record := newRecorder(f, subjectID)
	base := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(f.Seed) * time.Second)
	providerID := "fixture.costledger." + stableSuffix(subjectID)
	modelID := "fixture-embedding-model"
	if _, err := ledger.GetWallet(ctx, providerID); errors.Is(err, costledger.ErrWalletNotFound) {
		if _, err := ledger.SetBalance(ctx, providerID, modelpricing.USDFromDollars(10), base.Add(-time.Minute)); err != nil {
			return fixtures.RunOutcome{}, err
		}
	} else if err != nil {
		return fixtures.RunOutcome{}, err
	}

	orphan, err := r.embeddingInvocation(ctx, ledger, providerID, modelID, costledger.EmbeddingOperationRAGQuery, base)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	committed, err := r.embeddingInvocation(ctx, ledger, providerID, modelID, costledger.EmbeddingOperationMemorySearch, base.Add(time.Second))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	released, err := r.embeddingInvocation(ctx, ledger, providerID, modelID, costledger.EmbeddingOperationMemoryPropose, base.Add(2*time.Second))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	recent, err := r.embeddingInvocation(ctx, ledger, providerID, modelID, costledger.EmbeddingOperationRAGReindex, base.Add(31*time.Minute))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	missing, err := r.embeddingInvocation(ctx, ledger, providerID, modelID, costledger.EmbeddingOperationMemoryBackfill, base.Add(3*time.Second))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}

	orphanAmount := modelpricing.USDFromDollars(0.10)
	if err := ledger.ReserveEmbedding(ctx, providerID, orphan.ID, orphanAmount, base); err != nil {
		return fixtures.RunOutcome{}, err
	}
	afterFirst, err := ledger.GetWallet(ctx, providerID)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	// A retried call is the real idempotency exercise: the second Reserve
	// must neither create another event nor increase reserved_usd_nanos.
	if err := ledger.ReserveEmbedding(ctx, providerID, orphan.ID, orphanAmount, base.Add(time.Millisecond)); err != nil {
		return fixtures.RunOutcome{}, err
	}
	afterRetry, err := ledger.GetWallet(ctx, providerID)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("orphan_reservation_retry_never_double_counts_wallet", afterRetry.ReservedUSD == afterFirst.ReservedUSD)

	committedReserve := modelpricing.USDFromDollars(0.20)
	if err := ledger.ReserveEmbedding(ctx, providerID, committed.ID, committedReserve, base); err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := ledger.ReconcileEmbedding(ctx, providerID, committed.ID, modelpricing.USDFromDollars(0.15), base.Add(time.Second)); err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := ledger.ReserveEmbedding(ctx, providerID, released.ID, modelpricing.USDFromDollars(0.30), base); err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := ledger.ReleaseEmbedding(ctx, providerID, released.ID, base.Add(time.Second)); err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := ledger.ReserveEmbedding(ctx, providerID, recent.ID, modelpricing.USDFromDollars(0.40), base.Add(31*time.Minute)); err != nil {
		return fixtures.RunOutcome{}, err
	}

	// A reconciliation with no reservation must fail loudly. This checks
	// the public error path instead of assuming callers preserve the error.
	reconcileErr := ledger.ReconcileEmbedding(ctx, providerID, missing.ID, modelpricing.USDFromDollars(0.01), base.Add(4*time.Second))
	record.record("reconcile_error_is_never_silently_dropped", errors.Is(reconcileErr, costledger.ErrReservationNotFound))

	orphans, err := ledger.ListOrphanedReservations(ctx, base.Add(30*time.Minute), 1000)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	foundOrphan := false
	foundTerminal := false
	foundRecent := false
	for _, event := range orphans {
		if event.ProviderID != providerID || event.EmbeddingInvocationID == nil {
			continue
		}
		switch *event.EmbeddingInvocationID {
		case orphan.ID:
			foundOrphan = true
			record.outcome.EvidenceRefs = append(record.outcome.EvidenceRefs, fmt.Sprintf("wallet-event:%d", event.ID))
		case committed.ID, released.ID:
			foundTerminal = true
		case recent.ID:
			foundRecent = true
		}
	}
	record.record("orphan_is_visible_after_threshold", foundOrphan)
	record.record("orphan_is_never_confused_with_committed_or_released", !foundTerminal)
	record.record("recent_reservation_is_not_prematurely_orphaned", !foundRecent)

	events, err := ledger.ListEvents(ctx, providerID, 100)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	orphanReservations := 0
	for _, event := range events {
		if event.Kind == costledger.EventReserved && event.EmbeddingInvocationID != nil && *event.EmbeddingInvocationID == orphan.ID {
			orphanReservations++
		}
	}
	record.record("orphan_has_exactly_one_reserved_event", orphanReservations == 1)
	finalWallet, err := ledger.GetWallet(ctx, providerID)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	expectedReserved := orphanAmount + modelpricing.USDFromDollars(0.40)
	record.record("wallet_contains_only_live_reservations", finalWallet.ReservedUSD == expectedReserved)
	record.outcome.Metrics["orphan_count"] = boolMetric(foundOrphan)
	record.outcome.Metrics["reserved_usd_nanos"] = float64(finalWallet.ReservedUSD)
	record.outcome.Metrics["wallet_version"] = float64(finalWallet.Version)
	record.outcome.EvidenceRefs = append(record.outcome.EvidenceRefs,
		fmt.Sprintf("embedding-invocation:%d", orphan.ID),
		fmt.Sprintf("wallet:%s:reserved=%d", providerID, finalWallet.ReservedUSD),
	)
	return record.finish("reserva ambigua conservada y visible; no se ejecuta reconciliación automática"), nil
}

func (r Runner) embeddingInvocation(ctx context.Context, ledger costledger.Ledger, providerID, modelID string, operation costledger.EmbeddingOperation, createdAt time.Time) (costledger.EmbeddingInvocation, error) {
	var existing costledger.EmbeddingInvocation
	err := r.Store.Pool().QueryRow(ctx, `
SELECT id, organization_id, actor_role_id, task_id, provider_id, provider_model_id, billing_mode, operation, created_at
FROM embedding_invocations
WHERE organization_id=$1 AND actor_role_id=$2 AND provider_id=$3 AND provider_model_id=$4 AND operation=$5 AND created_at=$6
ORDER BY id LIMIT 1`, fixtureOrganization, fixtureActor, providerID, modelID, string(operation), createdAt.UTC()).Scan(
		&existing.ID, &existing.OrganizationID, &existing.ActorRoleID, &existing.TaskID, &existing.ProviderID,
		&existing.ProviderModelID, &existing.BillingMode, &existing.Operation, &existing.CreatedAt,
	)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return costledger.EmbeddingInvocation{}, err
	}
	return ledger.CreateEmbeddingInvocation(ctx, costledger.EmbeddingInvocation{
		OrganizationID: fixtureOrganization, ActorRoleID: fixtureActor, ProviderID: providerID,
		ProviderModelID: modelID, BillingMode: modelpricing.BillingOnline, Operation: operation, CreatedAt: createdAt.UTC(),
	})
}

type recorder struct {
	outcome fixtures.RunOutcome
	passed  bool
}

func newRecorder(f fixtures.Fixture, subjectID string) *recorder {
	return &recorder{passed: true, outcome: fixtures.RunOutcome{
		FixtureID: f.ID, SubjectID: subjectID, InvariantResults: map[string]bool{}, Metrics: map[string]float64{},
		EvidenceRefs: append([]string(nil), f.ExpectedEvidence...),
	}}
}

func (r *recorder) record(name string, passed bool) {
	r.outcome.InvariantResults[name] = passed
	if !passed {
		r.passed = false
		r.outcome.ViolatedInvariants = append(r.outcome.ViolatedInvariants, name)
	}
}

func (r *recorder) finish(notes string) fixtures.RunOutcome {
	r.outcome.Passed = r.passed
	r.outcome.Notes = notes
	return r.outcome
}

func stableSuffix(subjectID string) string {
	sum := sha256.Sum256([]byte(subjectID))
	return hex.EncodeToString(sum[:6])
}

func boolMetric(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
