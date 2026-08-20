package endtoendfixtures

import (
	"bytes"
	"context"
	"fmt"
	"time"

	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	agentmessagingpostgres "github.com/Mireuz13/explorarte-organization/internal/agentmessaging/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	"github.com/Mireuz13/explorarte-organization/internal/evaluationdb"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	memorypostgres "github.com/Mireuz13/explorarte-organization/internal/memory/postgres"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// Runner drives one real task from investigacion's research evidence
// through internal/executive's CEO->leader->worker->review->closure
// dispatch, against real PostgreSQL-backed tasks/decisiongraph/
// agentmessaging/agentbudget/authorization/completion services — the same
// components internal/executive's own postgres integration test uses. The
// only fake is the model/LLM call (executive.ModelCoordinator): this
// fixture must never make a real, billed provider call.
type Runner struct{ Store *platformpostgres.Store }

var _ fixtures.Runner = Runner{}

func (Runner) Supports(f fixtures.Fixture) bool {
	return f.ID == fixtureEndToEnd && f.RunnerKind == "end-to-end" && f.Status == fixtures.StatusRunnerReady
}

func (r Runner) Run(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	if err := evaluationdb.RequireDisposable(ctx, r.Store); err != nil {
		return fixtures.RunOutcome{}, err
	}
	scenario, ok := f.Scenario.(*Scenario)
	if !ok || scenario.FixtureID != f.ID {
		return fixtures.RunOutcome{}, fmt.Errorf("fixture %s was not activated by endtoendfixtures.Activate", f.ID)
	}
	record := newRecorder(f, subjectID)

	h, err := newHarness(ctx, r.Store)
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("bootstrap harness: %w", err)
	}
	ragStore, err := ragpostgres.New(r.Store, fixtureOrganization)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	memoryStore, err := memorypostgres.New(r.Store, fixtureOrganization)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}

	suffix := stableSuffix(subjectID)
	clock := &fixedClock{now: time.Unix(f.Seed, 0).UTC()}
	namespaceID := "eval_e2e_" + suffix
	evidence, err := seedResearchEvidence(ctx, r.Store, ragStore, memoryStore, clock, namespaceID, suffix)
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("seed research evidence: %w", err)
	}
	record.record("research_evidence_is_approved_before_executive_dispatch", evidence.ragChunkID != "" && evidence.memoryEntryID != "")

	budgetLedger, err := agentbudgetpostgres.New(r.Store)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	messageLedger, err := agentmessagingpostgres.New(r.Store, h.registry, h.authorizer, 1_000_000, 24*time.Hour)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	// EXEC-PRINCIPAL-001: no principal registration here anymore.
	// AgentMessages resolves (and lazily provisions, on first use) the
	// correct role-bound principal per sender internally -- the same
	// mechanism internal/executive/bootstrap/runtime.go now wires in
	// production. Registering one fixed principal up front, as this fixture
	// used to, only worked for a single sender role and silently diverged
	// from how production actually authenticates a multi-hop flow.
	dispatchStore, err := dispatchpostgres.New(r.Store)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	models := newFakeModelRuntime(evidence)
	// The role-bound principal resolver is real, against the same dispatch
	// store production uses: the fixture claims its leases with the same
	// canonical execution principals a real run would, so lease identity is
	// exercised rather than stubbed.
	roleBoundResolver, err := runtimeadapter.NewRoleBoundPrincipalResolver(dispatchStore, fixtureOrganization)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	orchestrator, err := executive.NewOrchestrator(
		executive.Dependencies{
			OrganizationID: fixtureOrganization,
			Registry:       runtimeadapter.Registry{Reader: h.registry, OrganizationID: fixtureOrganization},
			Tasks:          runtimeadapter.Tasks{Service: h.tasks, OrganizationID: fixtureOrganization},
			Contexts:       &fakeContext{},
			Assignments:    fakeAssignments{},
			Principals:     runtimeadapter.RoleBoundPrincipals{Resolver: roleBoundResolver},
			Models:         models,
			Harness:        models,
			Budget: runtimeadapter.ModelCallBudget{
				Models: models,
				Tasks:  runtimeadapter.Tasks{Service: h.tasks, OrganizationID: fixtureOrganization},
				Limits: executive.DefaultLimits(),
			},
			Completion:    h.completion,
			Decisions:     h.decisions,
			Authorization: h.authz,
			Limits:        executive.DefaultLimits(),
			Clock:         executive.ClockFunc(time.Now),
		},
		executive.WithAgentBudgets(runtimeadapter.AgentBudgets{Ledger: budgetLedger}),
		executive.WithAgentMessaging(runtimeadapter.AgentMessages{
			Ledger: messageLedger, MaxAttempts: 10,
			PrincipalStore: dispatchStore, OrganizationID: fixtureOrganization,
		}),
	)
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("construct orchestrator: %w", err)
	}

	// The idempotency key is deliberately NOT derived only from subjectID
	// (unlike the RAG/Memory evidence above, which legitimately benefits
	// from stable dedup against the same underlying content). This
	// fixture's fakeModelRuntime holds its invocation history only in Go
	// memory, fresh on every Run call — unlike a real, durable model
	// runtime. If Submit reused an existing root task from an earlier
	// invocation (same subjectID, same key), Resume would drive that
	// EXISTING task tree against a fake with no memory of what it already
	// returned for it, producing exactly the kind of stuck/incoherent run
	// this invariant would then wrongly blame on the orchestrator. A
	// per-invocation key sidesteps that: this fixture's own replay safety
	// is "never conflicts with a prior run," not "resumes across
	// invocations," which only the fake model runtime — not the real
	// production orchestrator — is unable to support.
	run, reused, err := orchestrator.Submit(ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: fmt.Sprintf("r30-14-%s-%d", suffix, time.Now().UnixNano()),
		Goal: executive.OwnerGoal{
			Goal:               "Analizar el caso sintetico r30-14 citando la evidencia de investigacion aprobada.",
			AcceptanceCriteria: []string{"un departamento revisado", "cierre verificado"},
		},
	})
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("submit executive run: %w", err)
	}
	if reused {
		return fixtures.RunOutcome{}, fmt.Errorf("submit executive run: unexpected idempotent reuse of a fresh key")
	}
	rootID := run.RootTaskID
	for i := 0; i < 20; i++ {
		run, err = orchestrator.Resume(ctx, rootID)
		if err != nil {
			return fixtures.RunOutcome{}, fmt.Errorf("resume executive run: %w", err)
		}
		if run.State == executive.StateCompleted || run.State == executive.StateFailed || run.State == executive.StateBlocked {
			break
		}
	}
	if run.State != executive.StateCompleted {
		if status, statusErr := orchestrator.Status(ctx, rootID); statusErr == nil {
			run = status
		}
	}
	record.record("executive_run_closes_completed_with_a_real_answer", run.State == executive.StateCompleted && run.AnswerToOwner != "")

	root, err := h.tasks.GetTask(ctx, rootID)
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("load root task: %w", err)
	}
	record.record("root_task_status_is_completed", root.Task.Status == tasks.StatusCompleted)
	if root.Task.CorrelationID == nil {
		return fixtures.RunOutcome{}, fmt.Errorf("root task %d has no correlation id", rootID)
	}

	var verificationLabel string
	if err := r.Store.Pool().QueryRow(ctx, `
SELECT dr.verification_label FROM decision_records dr
JOIN decision_graph_runs g ON g.id = dr.run_id
WHERE g.task_id=$1 ORDER BY dr.id DESC LIMIT 1`, rootID).Scan(&verificationLabel); err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("read root decision record: %w", err)
	}
	record.record("dag_terminal_decision_is_verified_not_inferred", verificationLabel == "verified")

	correlated, err := h.tasks.ListTasks(ctx, tasks.TaskFilter{OrganizationID: fixtureOrganization, CorrelationID: *root.Task.CorrelationID, Limit: 100})
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("list correlated tasks: %w", err)
	}
	var leaderTaskID, workerTaskID int64
	for _, task := range correlated {
		if task.AssignedRoleID == departmentLeaderRole {
			leaderTaskID = task.ID
		}
		if task.AssignedRoleID == departmentWorkerRole {
			workerTaskID = task.ID
		}
	}
	record.record("orchestration_reaches_a_department_leader_and_worker", leaderTaskID != 0 && workerTaskID != 0)

	ceoToLeader, err := countDelegationMessages(ctx, r.Store, rootID, leaderTaskID)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	leaderToWorker, err := countDelegationMessages(ctx, r.Store, leaderTaskID, workerTaskID)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("agent_messaging_delegates_across_every_hop", ceoToLeader == 1 && leaderToWorker == 1)

	var rootBudgetID int64
	budgetErr := r.Store.Pool().QueryRow(ctx, `SELECT id FROM agent_budgets WHERE task_id=$1`, rootID).Scan(&rootBudgetID)
	record.record("agent_budget_is_tracked_for_the_run", budgetErr == nil && rootBudgetID > 0)

	record.record("worker_result_cites_real_sourced_research_evidence", workerResultCitesRealEvidence(models, workerTaskID, evidence))

	record.outcome.Metrics["root_task_id"] = float64(rootID)
	record.outcome.Metrics["leader_task_id"] = float64(leaderTaskID)
	record.outcome.Metrics["worker_task_id"] = float64(workerTaskID)
	record.outcome.EvidenceRefs = append(record.outcome.EvidenceRefs,
		evidence.ragEvidenceRef(), evidence.memoryEvidenceRef(),
		fmt.Sprintf("task:%d:decision-verification=%s", rootID, verificationLabel),
	)
	return record.finish("caso real: investigacion (RAG/Memory aprobados) -> ingenieria_ia (lider/worker) -> ejecutivo CEO, cerrado con verificacion, mensajeria y presupuesto reales"), nil
}

func countDelegationMessages(ctx context.Context, store *platformpostgres.Store, senderTaskID, recipientTaskID int64) (int, error) {
	if senderTaskID == 0 || recipientTaskID == 0 {
		return 0, nil
	}
	var count int
	err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM agent_messages WHERE sender_task_id=$1 AND recipient_task_id=$2 AND message_type='delegation'`, senderTaskID, recipientTaskID).Scan(&count)
	return count, err
}

// workerResultCitesRealEvidence re-parses the persisted invocation bytes
// the orchestrator actually dispatched for workerTaskID — proving the
// evidence sourced in seedResearchEvidence genuinely flowed into what the
// executive worker stage produced, not merely that both halves
// independently exist.
func workerResultCitesRealEvidence(models *fakeModelRuntime, workerTaskID int64, evidence researchEvidence) bool {
	for _, invocations := range models.byAttempt {
		for _, invocation := range invocations {
			if invocation.TaskID != workerTaskID {
				continue
			}
			result, ok := models.results[invocation.ID]
			if !ok {
				continue
			}
			if bytes.Contains(result.JSONOutput, []byte(evidence.ragEvidenceRef())) && bytes.Contains(result.JSONOutput, []byte(evidence.memoryEvidenceRef())) {
				return true
			}
		}
	}
	return false
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
