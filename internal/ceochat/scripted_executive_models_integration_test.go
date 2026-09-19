//go:build integration

package ceochat_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// scriptedExecutiveModels stands in for the Executive's model port with one
// valid, deterministic output per purpose -- the minimal happy path (one
// department, one worker) -- and durable-looking per-attempt invocations. It is
// the same shape the Executive package's own PostgreSQL integration harness
// uses; the Executive, Task Engine, AgentBudget ledger, dispatch assignment
// provisioner and Campaign services around it are all real.
type scriptedExecutiveModels struct {
	mu        sync.Mutex
	nextID    int64
	byAttempt map[string]executive.InvocationRecord
	results   map[int64]executive.InvocationResult
	purposes  []string
}

func newScriptedExecutiveModels() *scriptedExecutiveModels {
	return &scriptedExecutiveModels{byAttempt: map[string]executive.InvocationRecord{}, results: map[int64]executive.InvocationResult{}, nextID: 9000}
}

func (s *scriptedExecutiveModels) executedPurposes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.purposes...)
}

func (s *scriptedExecutiveModels) Execute(_ context.Context, command executive.HarnessRunCommand) (executive.HarnessRunOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%d/%d", command.TaskID, command.AttemptID)
	if existing, ok := s.byAttempt[key]; ok {
		return s.outcome(existing), nil
	}
	purpose := command.Purpose.LegacyPurpose()
	s.nextID++
	invocation := executive.InvocationRecord{
		ID: s.nextID, TaskID: command.TaskID, AttemptID: command.AttemptID, SubjectRoleID: command.RoleID,
		Status: "succeeded", CorrelationID: command.CorrelationID, CausationID: command.CausationID,
		ContextSnapshotID: command.Context.ID,
	}
	s.byAttempt[key] = invocation
	s.purposes = append(s.purposes, purpose)
	body := scriptedExecutiveOutput(purpose)
	hash := sha256.Sum256(body)
	s.results[invocation.ID] = executive.InvocationResult{
		InvocationID: invocation.ID, JSONOutput: body, ResponseHash: hex.EncodeToString(hash[:]), ResponseBytes: len(body),
	}
	return s.outcome(invocation), nil
}

func (s *scriptedExecutiveModels) outcome(invocation executive.InvocationRecord) executive.HarnessRunOutcome {
	return executive.HarnessRunOutcome{
		Status: executive.HarnessRunSucceeded, InvocationID: invocation.ID,
		FinalOutput: string(s.results[invocation.ID].JSONOutput),
	}
}

func (s *scriptedExecutiveModels) GetInvocation(_ context.Context, id int64) (executive.InvocationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range s.byAttempt {
		if value.ID == id {
			return value, nil
		}
	}
	return executive.InvocationRecord{}, errors.New("invocation not found")
}

func (s *scriptedExecutiveModels) FindTaskAttemptInvocations(_ context.Context, taskID, attemptID int64) ([]executive.InvocationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.byAttempt[fmt.Sprintf("%d/%d", taskID, attemptID)]; ok {
		return []executive.InvocationRecord{value}, nil
	}
	return nil, nil
}

func (s *scriptedExecutiveModels) GetResult(_ context.Context, invocationID int64) (executive.InvocationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.results[invocationID]
	if !ok {
		return executive.InvocationResult{}, errors.New("result not found")
	}
	return value, nil
}

func (s *scriptedExecutiveModels) ProviderFailureRetryable(context.Context, int64) (bool, error) {
	return false, nil
}

func scriptedExecutiveOutput(purpose string) json.RawMessage {
	switch purpose {
	case "executive_ceo_plan":
		return json.RawMessage(`{"schema_version":"executive-plan/v1","objective":"analyze","department_requests":[{"unit_id":"ingenieria_ia","objective":"inspect","deliverable":"report","priority":10,"constraints":[]}],"global_constraints":[],"success_criteria":["verified"],"owner_decisions_required":[]}`)
	case "department_plan":
		return json.RawMessage(`{"schema_version":"department-plan/v2","department_id":"ingenieria_ia","tasks":[{"client_key":"inspect","assigned_role_id":"ingenieria_ia/qa","title":"Inspect state","instructions":"Inspect the bounded task context and report findings.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":5}],"review_criteria":["findings verified"],"unresolved":[]}`)
	case "department_worker":
		return json.RawMessage(`{"schema_version":"worker-result/v1","summary":"bounded findings","evidence_refs":["integration:evidence:1"]}`)
	case "department_review":
		return json.RawMessage(`{"schema_version":"department-review/v2","verdict":"accept","findings":["criteria satisfied"],"unsatisfied_criteria":[],"evidence_refs":[],"proposed_followup_tasks":[]}`)
	case "executive_ceo_closure":
		return json.RawMessage(`{"schema_version":"executive-closure/v1","status":"completed","answer_to_owner":"The requested area was analyzed with verified evidence.","completed_items":["engineering analysis"],"blocked_items":[],"unresolved_decisions":[],"evidence_refs":["integration:evidence:1"]}`)
	default:
		return json.RawMessage(`{"schema_version":"worker-result/v1","summary":"unknown","evidence_refs":[]}`)
	}
}
