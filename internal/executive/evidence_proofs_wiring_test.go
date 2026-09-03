package executive

import (
	"context"
	"testing"
)

// fakeEvidenceProofStore is an in-memory EvidenceProofStore double for
// orchestration-level tests: it proves how probeAdjudicationRequirements
// USES the interface (excludes already-proven slots, mints newly-covered
// ones), which the real Postgres integration test
// (internal/executive/postgres/evidence_proofs_integration_test.go) cannot
// exercise since it never runs a campaign.
type fakeEvidenceProofStore struct {
	// seeded are proofs presented as already valid, matched by BaseSHA only
	// (rootTaskID isolation is the Postgres store's own concern, already
	// proven against a real database).
	seeded []EvidenceProof
	minted []EvidenceProof
}

func (f *fakeEvidenceProofStore) ValidProofs(_ context.Context, _ int64, baseSHA string) (map[EvidenceSlot]EvidenceProof, error) {
	out := map[EvidenceSlot]EvidenceProof{}
	for _, proof := range f.seeded {
		if proof.BaseSHA == baseSHA {
			out[EvidenceSlot{Subject: proof.Subject, Relation: proof.Relation}] = proof
		}
	}
	return out, nil
}

func (f *fakeEvidenceProofStore) MintProof(_ context.Context, proof EvidenceProof) error {
	f.minted = append(f.minted, proof)
	return nil
}

func (f *fakeEvidenceProofStore) InvalidateProofs(_ context.Context, _ int64, _ string) error {
	return nil
}

// A slot with an already-valid proof must never reach the repository sensor
// again: capabilityWorld() here is EMPTY (no worlds registered for
// targetSHA at all), so if probeAdjudicationRequirements tried to probe
// MaxDesignRounds, the plan would come back Undelivered and the round-2
// obligation would never be adopted. It IS adopted -- and the sensor is
// never even queried -- because the pre-seeded proof excludes the slot
// before PlanSlots is ever called.
func TestAProvenSlotIsExcludedFromTheProbeAndNeverTouchesTheSensor(t *testing.T) {
	source := &probeWorldSource{worlds: map[string]map[string]string{}}
	// evidenceRequirementsForRound accumulates cumulatively: round 2's probe
	// covers both goalReqs' MaxDesignRounds (round 1) and driveDesignFreeze
	// (freshly demanded by adjudicationEvidence below), so both must be
	// pre-proven for the sensor to stay untouched.
	store := &fakeEvidenceProofStore{seeded: []EvidenceProof{
		{Subject: "MaxDesignRounds", Relation: "definition", BaseSHA: targetSHA,
			SourceReference: "repository://explorarte-organization@" + targetSHA + "/internal/executive/types.go#L1-L2",
			ContentDigest:   "seed-def"},
		{Subject: "MaxDesignRounds", Relation: "application", BaseSHA: targetSHA,
			SourceReference: "repository://explorarte-organization@" + targetSHA + "/internal/executive/orchestrator.go#L1-L2",
			ContentDigest:   "seed-app"},
		{Subject: "driveDesignFreeze", Relation: "definition", BaseSHA: targetSHA,
			SourceReference: "repository://explorarte-organization@" + targetSHA + "/internal/executive/design_freeze_phase.go#L1-L2",
			ContentDigest:   "seed-freeze-def"},
		{Subject: "driveDesignFreeze", Relation: "application", BaseSHA: targetSHA,
			SourceReference: "repository://explorarte-organization@" + targetSHA + "/internal/executive/design_freeze_phase.go#L3-L4",
			ContentDigest:   "seed-freeze-app"},
	}}
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", source), WithEvidenceProofs(store))
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`
	fixture.harness.adjudicationEvidence =
		`[{"subject":"driveDesignFreeze","relations":["definition","application"]}]`

	fixture.driveUntilStopped(t, 24)

	if !hasRoundRequirements(t, fixture, 2) {
		t.Fatal("a slot covered entirely by existing proofs was not adopted for round 2")
	}
	if len(source.seen) != 0 {
		t.Fatalf("a proven slot still reached the repository sensor: %v", source.seen)
	}
}

// A slot with NO existing proof, once genuinely covered by a successful
// probe, must be minted durably so a future round can skip re-paying its
// raw evidence cost.
func TestANewlyCoveredSlotIsMintedAfterTheProbeSucceeds(t *testing.T) {
	store := &fakeEvidenceProofStore{}
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", capabilityWorld()), WithEvidenceProofs(store))
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`
	fixture.harness.adjudicationEvidence =
		`[{"subject":"driveDesignFreeze","relations":["definition","application"]}]`

	fixture.driveUntilStopped(t, 24)

	if !hasRoundRequirements(t, fixture, 2) {
		t.Fatal("a supplyable obligation was not adopted for round 2")
	}
	want := map[EvidenceSlot]bool{
		{Subject: "MaxDesignRounds", Relation: "definition"}:  false,
		{Subject: "MaxDesignRounds", Relation: "application"}: false,
	}
	for _, proof := range store.minted {
		if proof.BaseSHA != targetSHA {
			t.Fatalf("minted proof at wrong base_sha: %+v", proof)
		}
		if proof.SourceReference == "" || proof.ContentDigest == "" {
			t.Fatalf("minted proof missing provenance: %+v", proof)
		}
		slot := EvidenceSlot{Subject: proof.Subject, Relation: proof.Relation}
		if _, ok := want[slot]; ok {
			want[slot] = true
		}
	}
	for slot, seen := range want {
		if !seen {
			t.Fatalf("slot %+v was covered but never minted", slot)
		}
	}
}
