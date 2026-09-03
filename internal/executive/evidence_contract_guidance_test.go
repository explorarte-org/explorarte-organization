package executive

import (
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-017-R8 closed as PROMPT_CONTRACT_MISMATCH #2: the host judged
// the worker's artifact against evidence slots it had never stated. The
// instructions asked for prose citations, the schema gave evidence[] its shape
// but not its obligations, and every rejection arrived only after a failed
// attempt -- three attempts died measuring a contract they were never handed.
//
// These guards pin the repair on both ends of that gap: the authoritative
// obligation list (the same slice ValidateEvidenceSupply and
// ValidateEvidenceStructure judge with) is rendered into the run's
// ExecutionContract before the model answers, and nothing about that guidance
// leaks into durable instructions or into what retrieval searches for.

// The rendering is derived from whatever list it is handed, never from a
// literal copy of one campaign's obligations.
func TestEvidenceContractGuidanceRendersTheAuthoritativeSlots(t *testing.T) {
	if got := evidenceContractGuidance(nil, nil); got != "" {
		t.Fatalf("no obligations must produce no guidance, got %q", got)
	}

	required := []EvidenceRequirement{
		{Subject: "MaxDesignRounds", Relations: []string{"application", "definition"}},
		{Subject: "MaxDepartmentReplans", Relations: []string{"definition", "application"}},
	}
	guidance := evidenceContractGuidance(required, nil)

	for _, want := range []string{
		`- subject="MaxDesignRounds", relation="definition"`,
		`- subject="MaxDesignRounds", relation="application"`,
		`- subject="MaxDepartmentReplans", relation="definition"`,
		`- subject="MaxDepartmentReplans", relation="application"`,
		"do not invent repository refs",
		"its ref must identify repository evidence supplied in this execution",
		// The subset rule is what refsAreAllStructured enforces. Stating an
		// equality rule would make the prompt stricter than the host.
		"every ref you put in evidence_refs must also occur in evidence[].ref",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("guidance missing %q:\n%s", want, guidance)
		}
	}
	if strings.Contains(guidance, "evidence_refs must be exactly") {
		t.Errorf("guidance claims an equality the validator does not enforce:\n%s", guidance)
	}

	permuted := []EvidenceRequirement{
		{Subject: "MaxDepartmentReplans", Relations: []string{"application", "definition"}},
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	}
	if again := evidenceContractGuidance(permuted, nil); again != guidance {
		t.Fatalf("guidance is not deterministic under input ordering:\n--- first ---\n%s\n--- again ---\n%s", guidance, again)
	}
}

// THE R8 CRITERION, end to end: a worker governed by four evidence slots sees
// those exact slots in its execution contract BEFORE it answers -- rendered
// from the durable obligations the host itself will judge the artifact with,
// reaching the real run command and never the durable instructions nor the
// repository selection text.
func TestWorkerRunCarriesTheRequiredEvidenceSlotsInTheRealRequest(t *testing.T) {
	replansDefRef := "repository://explorarte-organization@" + targetSHA + "/internal/executive/budget.go#L31-L40"
	supply := append(fullSupply(),
		wiringSource(replansDefRef, "\n// MaxDepartmentReplans bounds department replanning.\nMaxDepartmentReplans int\n"),
	)
	fixture := newWiringFixture(t, "freeze", supply, []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
		{Subject: "MaxDepartmentReplans", Relations: []string{"definition", "application"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `","` + replansDefRef + `","` + replansAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"},` +
			`{"claim":"replan bound declared","subject":"MaxDepartmentReplans","relation":"definition","ref":"` + replansDefRef + `"},` +
			`{"claim":"replan bound applied","subject":"MaxDepartmentReplans","relation":"application","ref":"` + replansAppRef + `"}]}`

	run, err := fixture.driveUntilStopped(t, 24)
	if err != nil {
		t.Fatalf("a fully guided world failed: %v", err)
	}
	if run.State == StateBlocked {
		t.Fatalf("a fully guided world blocked: %+v", run)
	}
	command, ok := fixture.commandFor(PurposeDepartmentWorker)
	if !ok {
		t.Fatal("the worker never ran")
	}

	for _, want := range []string{
		"Required structured evidence slots for this result:",
		`- subject="MaxDesignRounds", relation="definition"`,
		`- subject="MaxDesignRounds", relation="application"`,
		`- subject="MaxDepartmentReplans", relation="definition"`,
		`- subject="MaxDepartmentReplans", relation="application"`,
		"every ref you put in evidence_refs must also occur in evidence[].ref",
	} {
		if !strings.Contains(command.ExecutionContract, want) {
			t.Errorf("worker ExecutionContract missing %q:\n%s", want, command.ExecutionContract)
		}
	}
	// Worker runs carry the evidence contract only; the task-class guidance
	// belongs to planning purposes and must not leak into them.
	if strings.Contains(command.ExecutionContract, "task_class MUST") {
		t.Errorf("task-class guidance leaked into a worker run:\n%s", command.ExecutionContract)
	}

	// The guidance is execution-time communication, not durable instruction:
	// a pre-existing TaskRecord needs no rewrite to be told its obligations.
	task, ok := designWorkerTask(t, fixture)
	if !ok {
		t.Fatal("no department worker task exists")
	}
	if strings.Contains(task.Instructions, "Required structured evidence slots") {
		t.Fatal("the evidence contract was baked into durable instructions")
	}

	// And retrieval stayed exactly what it would have been without it: the
	// selection text the snapshot was seeded from carries the subjects the
	// obligations name, not a word of the guidance prose.
	ctxPort := fixture.harness.contexts
	if ctxPort == nil {
		t.Fatal("fixture did not record context requests")
	}
	request, recorded := ctxPort.requests[command.Context.ID]
	if !recorded {
		t.Fatalf("no context request recorded for snapshot %d", command.Context.ID)
	}
	for _, seeded := range []string{"MaxDesignRounds", "MaxDepartmentReplans"} {
		found := false
		for _, subject := range request.RepositorySubjects {
			if subject == seeded {
				found = true
			}
		}
		if !found {
			t.Errorf("obligation subject %q was not seeded into retrieval: %v", seeded, request.RepositorySubjects)
		}
	}
	if strings.Contains(request.RepositoryQuery, "Required structured evidence slots") {
		t.Fatal("the evidence guidance changed what retrieval searched for -- the R5 pollution in a new costume")
	}
}

// V8's live failure (root 18978, CAPACITY-LIVENESS-CANARY-001): a real
// department worker was told the exact accepted repository:// ref in the
// GOAL prose, but the execution contract it was actually judged against only
// said "identify repository evidence supplied in this execution" -- so it
// re-searched its snapshot and cited a different, wrong excerpt for the same
// range twice over. This guard pins the repair: when the host already knows
// which ref(s) satisfy a slot, the contract states them verbatim, not just
// the slot's name.
func TestEvidenceContractGuidanceStatesTheExactAcceptedRefPerSlot(t *testing.T) {
	required := []EvidenceRequirement{
		{Subject: "RetryPolicy", Relations: []string{"definition", "application"}},
	}
	available := map[EvidenceSlot][]string{
		{Subject: "RetryPolicy", Relation: "definition"}:  {"repository://explorarte-organization@" + targetSHA + "/internal/tasks/backoff.go#L1-L29"},
		{Subject: "RetryPolicy", Relation: "application"}: {"repository://explorarte-organization@" + targetSHA + "/internal/tasks/backoff.go#L1-L29"},
	}
	guidance := evidenceContractGuidance(required, available)

	for _, want := range []string{
		`- subject="RetryPolicy", relation="definition"`,
		`- subject="RetryPolicy", relation="application"`,
		`"repository://explorarte-organization@` + targetSHA + `/internal/tasks/backoff.go#L1-L29"`,
		"copy one character-for-character",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("guidance missing %q:\n%s", want, guidance)
		}
	}

	// A slot the host has no known ref for yet still names the slot, but
	// asserts no accepted ref -- there is nothing to copy, and the guidance
	// must not imply otherwise.
	unresolved := evidenceContractGuidance(required, nil)
	if strings.Contains(unresolved, "accepted ref(s) for this exact slot") {
		t.Fatalf("guidance claimed an accepted ref when available was nil:\n%s", unresolved)
	}
	if !strings.Contains(unresolved, `- subject="RetryPolicy", relation="definition"`) {
		t.Fatalf("guidance dropped the slot itself when no ref is known:\n%s", unresolved)
	}
}

// THE V8 CRITERION, end to end: a real worker run's ExecutionContract states
// the exact ref the host's own classifier already found for each required
// slot -- the same map suppliedEvidence built and ValidateEvidenceSupply
// already validated the run against, not a second, independent guess.
func TestWorkerRunCarriesTheExactAcceptedRefInTheRealRequest(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`

	if _, err := fixture.driveUntilStopped(t, 24); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	command, ok := fixture.commandFor(PurposeDepartmentWorker)
	if !ok {
		t.Fatal("the worker never ran")
	}
	for _, want := range []string{
		`"` + wiringDefRef + `"`,
		`"` + wiringAppRef + `"`,
		"copy one character-for-character",
	} {
		if !strings.Contains(command.ExecutionContract, want) {
			t.Errorf("worker ExecutionContract missing the accepted ref %q:\n%s", want, command.ExecutionContract)
		}
	}
}

// The contract follows the round's authoritative obligations, wherever they
// came from and however many there are: a campaign demanding one slot tells
// the worker about one slot, and names no other.
func TestEvidenceContractFollowsTheRequirementsNotALiteral(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `"],` +
			`"evidence":[{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"}]}`

	if _, err := fixture.driveUntilStopped(t, 24); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	command, ok := fixture.commandFor(PurposeDepartmentWorker)
	if !ok {
		t.Fatal("the worker never ran")
	}
	if !strings.Contains(command.ExecutionContract, `- subject="MaxDesignRounds", relation="definition"`) {
		t.Fatalf("contract missing the demanded slot:\n%s", command.ExecutionContract)
	}
	for _, unwanted := range []string{"MaxDepartmentReplans", `relation="application"`} {
		if strings.Contains(command.ExecutionContract, unwanted) {
			t.Fatalf("contract states %q though no such obligation exists:\n%s", unwanted, command.ExecutionContract)
		}
	}
}
