//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

func integrationDescriptorSpec(f *fixture, runID string) executionharness.RunSpec {
	content := "approved descriptor integration context"
	digest := sha256.Sum256([]byte(content))
	return executionharness.RunSpec{
		Identity: executionharness.RunIdentity{
			RunID: runID, OrganizationID: historyOrganization, TaskID: f.taskID,
			AttemptID: f.attemptID, RoleID: historyRole, ExecutionPrincipalID: "descriptor-principal",
			CorrelationID: runID + ":corr", CausationID: runID + ":cause",
		},
		LeaseToken: "descriptor-lease-token",
		Context:    executionharness.InitialContext{ID: "snapshot-1", Version: "v1", Digest: hex.EncodeToString(digest[:]), Content: content},
		Tools: []executionharness.ToolDefinition{{
			Name: "search", Description: "private description is not stored", InputSchema: []byte(`{"type":"object"}`),
		}},
		Policy: executionharness.RunPolicy{MaxTurns: 2, MaxToolCalls: 0, ExecutionProfileID: "profile/descriptor", ModelPolicyRef: "policy/descriptor", BuildRef: "build/descriptor"},
	}
}

func TestRunDescriptorPostgreSQL17IsDurableAndImmutable(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	spec := integrationDescriptorSpec(f, "descriptor-durable")
	descriptor, err := executionharness.BuildRunDescriptor(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.history.EnsureRunDescriptor(f.ctx, descriptor); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.history.ReadRunDescriptor(f.ctx, historyOrganization, spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := descriptor.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	gotDigest, err := loaded.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if gotDigest != wantDigest || loaded.TaskID != f.taskID || loaded.AttemptID != f.attemptID || loaded.ExecutionProfileID != spec.Policy.ExecutionProfileID {
		t.Fatalf("loaded descriptor=%+v digest=%s want digest=%s", loaded, gotDigest, wantDigest)
	}
	var storedTools string
	if err = f.store.Pool().QueryRow(f.ctx, `SELECT frozen_tools::text FROM execution_run_descriptors WHERE organization_id=$1 AND harness_run_id=$2`, historyOrganization, spec.Identity.RunID).Scan(&storedTools); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedTools, "private description") || strings.Contains(storedTools, "descriptor-lease-token") {
		t.Fatalf("descriptor row retained private/raw material: %s", storedTools)
	}
	if err = f.history.EnsureRunDescriptor(f.ctx, descriptor); err != nil {
		t.Fatalf("same descriptor was not idempotent: %v", err)
	}
	driftedSpec := spec
	driftedSpec.Policy.ExecutionProfileID = "profile/other"
	drifted, err := executionharness.BuildRunDescriptor(driftedSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.history.EnsureRunDescriptor(f.ctx, drifted); !errors.Is(err, executionharness.ErrRunDescriptorConflict) {
		t.Fatalf("identity drift error=%v want conflict", err)
	}
	if _, err = f.store.Pool().Exec(f.ctx, `UPDATE execution_run_descriptors SET build_ref='mutated' WHERE organization_id=$1 AND harness_run_id=$2`, historyOrganization, spec.Identity.RunID); err == nil {
		t.Fatal("descriptor UPDATE unexpectedly succeeded")
	}
	if _, err = f.store.Pool().Exec(f.ctx, `DELETE FROM execution_run_descriptors WHERE organization_id=$1 AND harness_run_id=$2`, historyOrganization, spec.Identity.RunID); err == nil {
		t.Fatal("descriptor DELETE unexpectedly succeeded")
	}
	for name, frozenTools := range map[string]string{
		"bad digest":     `[{"name":"search","definition_digest":"bad"}]`,
		"duplicate name": `[{"name":"search","definition_digest":"0000000000000000000000000000000000000000000000000000000000000000"},{"name":"search","definition_digest":"0000000000000000000000000000000000000000000000000000000000000000"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			_, insertErr := f.store.Pool().Exec(f.ctx, `
				INSERT INTO execution_run_descriptors(
					organization_id,harness_run_id,task_id,attempt_id,role_id,execution_principal_id,
					context_id,context_version,context_digest,execution_profile_id,model_policy_ref,
					build_ref,max_turns,max_tool_calls,frozen_tools,identity_digest,canonical_digest
				) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16,$17)`,
				historyOrganization, "descriptor-invalid-"+name, descriptor.TaskID, descriptor.AttemptID,
				descriptor.RoleID, descriptor.ExecutionPrincipalID, descriptor.ContextID, descriptor.ContextVersion,
				descriptor.ContextDigest, descriptor.ExecutionProfileID, descriptor.ModelPolicyRef, descriptor.BuildRef,
				descriptor.MaxTurns, descriptor.MaxToolCalls, frozenTools, descriptor.IdentityDigest,
				strings.Repeat("0", 64))
			if insertErr == nil {
				t.Fatal("invalid frozen tool reference was accepted")
			}
		})
	}
}

func TestRunDescriptorPostgreSQL17ConcurrentEnsureIsIdempotent(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	descriptor, err := executionharness.BuildRunDescriptor(integrationDescriptorSpec(f, "descriptor-race"))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 4
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- f.history.EnsureRunDescriptor(context.Background(), descriptor)
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent same-descriptor ensure failed: %v", err)
		}
	}
	var count int
	if err = f.store.Pool().QueryRow(f.ctx, `SELECT count(*) FROM execution_run_descriptors WHERE organization_id=$1 AND harness_run_id=$2`, historyOrganization, "descriptor-race").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("descriptor rows=%d want 1", count)
	}
}
