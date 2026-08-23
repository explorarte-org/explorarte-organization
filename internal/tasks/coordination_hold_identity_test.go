package tasks

import "testing"

// The request hash decides whether a reused idempotency key describes the same
// task or a contradictory one. The coordination hold is not part of that
// identity: it is a host instruction about how to sequence creation, and two
// requests differing only in it describe the same work.
//
// Stating it as a test rather than a comment matters because the failure mode
// is invisible until a deploy. A child created before the hold existed hashes
// without it; the resumed orchestrator now always asks for one. If the field
// entered the hash, neither the current nor the legacy computation would match
// the durable row, and every in-flight run would die with an idempotency
// conflict on its first existing child rather than resuming.
func TestCoordinationHoldIsNotPartOfTaskIdentity(t *testing.T) {
	unheld := CreateRequest{
		OrganizationID: "explorarte", AssignedRoleID: "ingenieria_ia/lider",
		TaskClass: "coordination.department_plan", IdempotencyKey: "child:plan",
		Title: "Department planning", Instructions: "plan",
		AcceptanceCriteria: []string{"Return one strict DepartmentPlan JSON value"},
	}
	held := unheld
	held.HoldForCoordination = true

	before, err := HashCreateRequest(unheld)
	if err != nil {
		t.Fatal(err)
	}
	after, err := HashCreateRequest(held)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("asking for a coordination hold changed the request hash (%s -> %s): a task created before the hold existed could no longer be resumed under its own idempotency key", before, after)
	}

	// The guard is only meaningful if the hash still reacts to things that
	// genuinely are identity. Without this, deleting every field from the
	// hash would pass the assertion above.
	reassigned := held
	reassigned.AssignedRoleID = "ingenieria_ia/qa"
	other, err := HashCreateRequest(reassigned)
	if err != nil {
		t.Fatal(err)
	}
	if other == after {
		t.Fatal("the request hash no longer distinguishes a different assignee; it has stopped protecting identity at all")
	}
}
