package executive

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
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
	all := append(append([]EvidenceProof(nil), f.seeded...), f.minted...)
	for _, proof := range all {
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

// V7's production failure: the current three subjects and the proposed five
// each fit independently, but their cumulative raw transport crossed the old
// ceiling. A durable proof store must turn that into a successful next round:
// old slots travel as exact refs in the execution contract, while only novel
// slots consume repository retrieval.
func TestProofBackedRoundCarriesOldSlotsAndRetrievesOnlyNovelSlots(t *testing.T) {
	world := &probeWorldSource{worlds: map[string]map[string]string{targetSHA: {
		"internal/executive/alpha.go": "package executive\n\nfunc Alpha() bool { return true }\n",
		"internal/executive/beta.go":  "package executive\n\nfunc Beta() int { return 1 }\n",
		"internal/executive/gamma.go": "package executive\n\nfunc Gamma() string { return \"\" }\n",
		"internal/executive/zeta.go":  "package executive\n\nfunc Zeta() byte { return 0 }\n",
	}}}
	ref := func(subject string) string {
		return "repository://explorarte-organization@" + targetSHA + "/internal/executive/" + strings.ToLower(subject) + ".go#L1-L4"
	}
	sources := []SnapshotSource{
		wiringSource(ref("Alpha"), "\nfunc Alpha() bool { return true }\n"),
		wiringSource(ref("Beta"), "\nfunc Beta() int { return 1 }\n"),
		wiringSource(ref("Gamma"), "\nfunc Gamma() string { return \"\" }\n"),
		wiringSource(ref("Zeta"), "\nfunc Zeta() byte { return 0 }\n"),
	}
	store := &fakeEvidenceProofStore{}
	fixture := newWiringFixture(t, "revise", sources, []EvidenceRequirementProposal{
		{Subject: "Alpha", Relations: []string{"definition"}},
		{Subject: "Beta", Relations: []string{"definition"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", world), WithEvidenceProofs(store))
	fixture.harness.adjudicationEvidence =
		`[{"subject":"Gamma","relations":["definition"]},` +
			`{"subject":"Zeta","relations":["definition"]}]`
	fixture.harness.adjudicationVerdictByRound = map[int]string{1: "revise", 2: "freeze"}
	fixture.harness.departmentWorkerBody = func(task TaskRecord) string {
		round := designRoundOf(task.IdempotencyKey)
		if round == 1 {
			return workerResultForSlots(map[EvidenceSlot]string{
				{Subject: "Alpha", Relation: "definition"}: ref("Alpha"),
				{Subject: "Beta", Relation: "definition"}:  ref("Beta"),
			})
		}
		proofs, err := store.ValidProofs(context.Background(), fixture.root, targetSHA)
		if err != nil {
			t.Fatal(err)
		}
		return workerResultForSlots(map[EvidenceSlot]string{
			{Subject: "Alpha", Relation: "definition"}: proofs[EvidenceSlot{Subject: "Alpha", Relation: "definition"}].SourceReference,
			{Subject: "Beta", Relation: "definition"}:  proofs[EvidenceSlot{Subject: "Beta", Relation: "definition"}].SourceReference,
			{Subject: "Gamma", Relation: "definition"}: ref("Gamma"),
			{Subject: "Zeta", Relation: "definition"}:  ref("Zeta"),
		})
	}

	original := jointAdmissionLimits
	jointAdmissionLimits = func() repositoryevidence.Limits {
		return repositoryevidence.Limits{MaxFiles: 8, MaxRanges: 16, MaxBytes: 96 * 1024, MaxSearches: 2, MaxLines: 400}
	}
	defer func() { jointAdmissionLimits = original }()

	driveCapability(t, fixture, 32)
	if !hasRoundRequirements(t, fixture, 2) {
		t.Fatal("independently fitting current and novel sets did not open round 2")
	}

	var roundTwo HarnessRunCommand
	for _, command := range fixture.harness.commands {
		if command.Purpose != PurposeDepartmentWorker {
			continue
		}
		task, err := fixture.tasks.GetTask(context.Background(), command.TaskID)
		if err == nil && designRoundOf(task.IdempotencyKey) == 2 {
			roundTwo = command
			break
		}
	}
	if roundTwo.TaskID == 0 {
		t.Fatal("round-2 worker never ran")
	}
	request := fixture.harness.contexts.requests[roundTwo.Context.ID]
	gotSlots := map[EvidenceSlot]bool{}
	for _, slot := range request.RepositorySlots {
		gotSlots[slot] = true
	}
	for _, old := range []EvidenceSlot{{Subject: "Alpha", Relation: "definition"}, {Subject: "Beta", Relation: "definition"}} {
		if gotSlots[old] {
			t.Fatalf("proof-backed slot was re-transported in round 2: %+v", old)
		}
		proof := findProof(store.minted, old)
		if proof.SourceReference == "" || !strings.Contains(roundTwo.ExecutionContract, proof.SourceReference) {
			t.Fatalf("proof-backed slot missing from execution contract: %+v", old)
		}
	}
	for _, novel := range []EvidenceSlot{{Subject: "Gamma", Relation: "definition"}, {Subject: "Zeta", Relation: "definition"}} {
		if !gotSlots[novel] {
			t.Fatalf("novel slot was not retrieved in round 2: %+v", novel)
		}
	}
	task, err := fixture.tasks.GetTask(context.Background(), roundTwo.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "completed" {
		t.Fatalf("proof-backed round-2 worker ended %q: %s", task.Status, task.Reason)
	}
}

func workerResultForSlots(slots map[EvidenceSlot]string) string {
	ordered := make([]EvidenceSlot, 0, len(slots))
	for slot := range slots {
		ordered = append(ordered, slot)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Subject != ordered[j].Subject {
			return ordered[i].Subject < ordered[j].Subject
		}
		return ordered[i].Relation < ordered[j].Relation
	})
	refs, items := make([]string, 0, len(ordered)), make([]string, 0, len(ordered))
	for _, slot := range ordered {
		refs = append(refs, slots[slot])
		items = append(items, `{"claim":"grounded","subject":"`+slot.Subject+`","relation":"`+slot.Relation+`","ref":"`+slots[slot]+`"}`)
	}
	return `{"schema_version":"worker-result/v2","summary":"Grounded.","evidence_refs":[` + refsJSON(refs) + `],"evidence":[` + strings.Join(items, ",") + `]}`
}

func findProof(proofs []EvidenceProof, slot EvidenceSlot) EvidenceProof {
	for _, proof := range proofs {
		if proof.Subject == slot.Subject && proof.Relation == slot.Relation {
			return proof
		}
	}
	return EvidenceProof{}
}
