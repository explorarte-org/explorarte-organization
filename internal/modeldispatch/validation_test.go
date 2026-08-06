package modeldispatch

import (
	"errors"
	"testing"
	"time"
)

func TestPrepareRegisterCommandValidation(t *testing.T) {
	valid := RegisterPrincipalCommand{OrganizationID: "explorarte", PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner", PrincipalKind: PrincipalLocalProcess, IdempotencyKey: "idem-1"}
	if _, err := PrepareRegisterCommand(valid); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*RegisterPrincipalCommand)
	}{
		{"empty principal key", func(c *RegisterPrincipalCommand) { c.PrincipalKey = "" }},
		{"principal key with spaces", func(c *RegisterPrincipalCommand) { c.PrincipalKey = "looks like a secret token" }},
		{"principal key too long", func(c *RegisterPrincipalCommand) { c.PrincipalKey = string(make([]byte, 201)) }},
		{"invalid role", func(c *RegisterPrincipalCommand) { c.DispatchActorRoleID = "Not A Role" }},
		{"invalid kind", func(c *RegisterPrincipalCommand) { c.PrincipalKind = "worker_thread" }},
		{"empty idempotency key", func(c *RegisterPrincipalCommand) { c.IdempotencyKey = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command := valid
			tc.mutate(&command)
			if _, err := PrepareRegisterCommand(command); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("expected ErrInvalidRequest, got %v", err)
			}
		})
	}
}

func TestPrepareCreateAssignmentCommandTTLAndBounds(t *testing.T) {
	now := mustTime("2026-01-01T00:00:00Z")
	base := CreateAssignmentCommand{OrganizationID: "explorarte", TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner", ExecutionPrincipalKey: "oracle-01/model-runtime-01", MaxInvocations: 1, IdempotencyKey: "assign-1"}

	t.Run("default TTL applies when neither valid_until nor ttl given", func(t *testing.T) {
		_, from, until, err := PrepareCreateAssignmentCommand(base, now, 15*time.Minute, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if !from.Equal(now) || !until.Equal(now.Add(15*time.Minute)) {
			t.Fatalf("from=%v until=%v", from, until)
		}
	})

	t.Run("explicit ttl is honored within max", func(t *testing.T) {
		ttl := 30 * time.Minute
		command := base
		command.TTL = &ttl
		_, _, until, err := PrepareCreateAssignmentCommand(command, now, 15*time.Minute, time.Hour)
		if err != nil || !until.Equal(now.Add(30*time.Minute)) {
			t.Fatalf("until=%v err=%v", until, err)
		}
	})

	t.Run("ttl exceeding max TTL is rejected", func(t *testing.T) {
		ttl := 2 * time.Hour
		command := base
		command.TTL = &ttl
		if _, _, _, err := PrepareCreateAssignmentCommand(command, now, 15*time.Minute, time.Hour); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected TTL ceiling rejection, got %v", err)
		}
	})

	t.Run("valid_until and ttl are mutually exclusive", func(t *testing.T) {
		ttl := 5 * time.Minute
		until := now.Add(10 * time.Minute)
		command := base
		command.TTL = &ttl
		command.ValidUntil = &until
		if _, _, _, err := PrepareCreateAssignmentCommand(command, now, 15*time.Minute, time.Hour); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected mutual exclusivity rejection, got %v", err)
		}
	})

	t.Run("valid_until in the past is rejected", func(t *testing.T) {
		past := now.Add(-time.Minute)
		command := base
		command.ValidUntil = &past
		if _, _, _, err := PrepareCreateAssignmentCommand(command, now, 15*time.Minute, time.Hour); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected past valid_until rejection, got %v", err)
		}
	})

	t.Run("max_invocations out of range is rejected", func(t *testing.T) {
		command := base
		command.MaxInvocations = 0
		if _, _, _, err := PrepareCreateAssignmentCommand(command, now, 15*time.Minute, time.Hour); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected quota rejection, got %v", err)
		}
		command.MaxInvocations = maxAssignmentQuota + 1
		if _, _, _, err := PrepareCreateAssignmentCommand(command, now, 15*time.Minute, time.Hour); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected quota ceiling rejection, got %v", err)
		}
	})

	t.Run("subject role must be well formed", func(t *testing.T) {
		command := base
		command.SubjectRoleID = "Not A Role"
		if _, _, _, err := PrepareCreateAssignmentCommand(command, now, 15*time.Minute, time.Hour); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected role rejection, got %v", err)
		}
	})
}

func TestValidateTaskAttemptForAssignment(t *testing.T) {
	now := mustTime("2026-01-01T00:00:00Z")
	ref := TaskAttemptRef{TaskID: 3, AttemptID: 4, OrganizationID: "explorarte", AssignedRoleID: "ingenieria_ia/code-runner", TaskStatus: "running", AttemptStatus: "running", LeaseHolderID: "worker-1", LeaseExpiresAt: now.Add(time.Hour)}
	if err := validateTaskAttemptForAssignment(ref, "explorarte", 3, 4, "ingenieria_ia/code-runner", now); err != nil {
		t.Fatalf("valid attempt rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*TaskAttemptRef)
	}{
		{"foreign subject", func(r *TaskAttemptRef) { r.AssignedRoleID = "ingenieria_ia/frontend" }},
		{"task not running", func(r *TaskAttemptRef) { r.TaskStatus = "completed" }},
		{"attempt not running", func(r *TaskAttemptRef) { r.AttemptStatus = "failed" }},
		{"no lease holder", func(r *TaskAttemptRef) { r.LeaseHolderID = "" }},
		{"expired lease", func(r *TaskAttemptRef) { r.LeaseExpiresAt = now.Add(-time.Second) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := ref
			tc.mutate(&mutated)
			if err := validateTaskAttemptForAssignment(mutated, "explorarte", 3, 4, "ingenieria_ia/code-runner", now); !errors.Is(err, ErrTaskAttemptRejected) {
				t.Fatalf("expected rejection, got %v", err)
			}
		})
	}
}

func TestEligibleDispatchActorRole(t *testing.T) {
	if !eligibleDispatchActorRole(RoleRef{Enabled: true, Executable: true, AuthorityClass: "execution_service"}) {
		t.Fatal("expected eligible role")
	}
	ineligible := []RoleRef{
		{Enabled: false, Executable: true, AuthorityClass: "execution_service"},
		{Enabled: true, Executable: false, AuthorityClass: "execution_service"},
		{Enabled: true, Executable: true, AuthorityClass: "specialist"},
	}
	for _, role := range ineligible {
		if eligibleDispatchActorRole(role) {
			t.Fatalf("role incorrectly eligible: %+v", role)
		}
	}
}
