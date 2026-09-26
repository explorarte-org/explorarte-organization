//go:build integration

// Audit 2026-09-26, finding 6, against real PostgreSQL: a review request whose task failed moves
// to failed, idempotently; a request whose task can still run stays pending, and a completed one
// is never touched.
package ceochat_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

func TestReviewRequestsFollowTheirTasksTerminalFailure(t *testing.T) {
	f, fx := newFinanceWorkerFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	service := f.withScriptedModel(t, oneShotFinalAnswerModel{})

	_, failedTaskID, failedRequestID := fx.seedReadyReviewTask(t, service, "reconcile-failed")
	_, readyTaskID, readyRequestID := fx.seedReadyReviewTask(t, service, "reconcile-ready")
	_, doneTaskID, doneRequestID := fx.seedReadyReviewTask(t, service, "reconcile-done")

	// The shape of production requests 2, 27 and 30: the attempt ends in a non-retryable failure.
	claimed, err := fx.tasksService.ClaimTaskByID(ctx, failedTaskID, tasks.ClaimRequest{
		OrganizationID: chatTestOrganization, WorkerID: "reconcile-test", AssignedRoleID: fx.reviewerRoleID, LeaseDuration: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	lease := tasks.LeaseCommand{TaskID: claimed.Task.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "reconcile-test"}
	if _, err = fx.tasksService.StartAttempt(ctx, lease); err != nil {
		t.Fatalf("start attempt: %v", err)
	}
	if _, err = fx.tasksService.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{
		LeaseCommand: lease,
		Result:       tasks.AttemptResult{Outcome: tasks.OutcomeNonRetryableFailure, FailureCode: "FINANCE_HARNESS_FAILED", Summary: "provider unavailable"},
	}); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	failedTask, err := fx.tasksService.GetTask(ctx, failedTaskID)
	if err != nil || failedTask.Task.Status != tasks.StatusFailed {
		t.Fatalf("the failed review task is %q (%v), want failed", failedTask.Task.Status, err)
	}

	if _, _, err = fx.financeService.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID: chatTestOrganization, TaskID: doneTaskID, ReviewRequestID: doneRequestID,
		WorkerID: "reconcile-test", MockOutput: &mockRecommended,
	}); err != nil {
		t.Fatalf("complete the done review: %v", err)
	}

	moved, err := fx.store.FailReviewRequestsOfTerminalTasks(ctx, chatTestOrganization, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(moved, failedRequestID) {
		t.Fatalf("request %d of failed task %d was not moved: %v", failedRequestID, failedTaskID, moved)
	}
	if slices.Contains(moved, readyRequestID) || slices.Contains(moved, doneRequestID) {
		t.Fatalf("a request whose task did not fail was moved: %v", moved)
	}
	for id, want := range map[int64]campaign.ReviewRequestStatus{
		failedRequestID: campaign.ReviewRequestStatusFailed,
		readyRequestID:  campaign.ReviewRequestStatusPending,
		doneRequestID:   campaign.ReviewRequestStatusCompleted,
	} {
		request, getErr := fx.store.GetReviewRequest(ctx, chatTestOrganization, id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if request.Status != want {
			t.Errorf("request %d is %q, want %q", id, request.Status, want)
		}
	}
	_ = readyTaskID

	again, err := fx.store.FailReviewRequestsOfTerminalTasks(ctx, chatTestOrganization, 128)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(again, failedRequestID) {
		t.Fatalf("the reconciliation is not idempotent: request %d moved twice", failedRequestID)
	}
}
