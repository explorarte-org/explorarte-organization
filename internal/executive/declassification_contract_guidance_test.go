package executive

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-017-R11 and -R13 died at the same gate without ever being
// shown it existed: DeclassifyCandidate refused the candidate bundle because
// the architect had quoted the very prose comment the goal asked about -- an
// exactly-48-normalized-character span, the threshold minimum. The gate acted
// within its design; the defect was that its rule lived only host-side. These
// guards pin the repair on both ends of that gap, the same way
// evidence_contract_guidance_test.go pinned b7cf98d: the gate's own rule,
// rendered beside the gate, reaches every producer BEFORE it answers, and
// nothing about that rendering leaks into durable instructions, retrieval, or
// executions that were never shown organizational source.

// The rendering derives its number from the gate's own threshold -- one
// authority, two ends -- and states the rule exactly as enforced, no
// stricter: paraphrase is the action, the run length is the condition,
// provenance metadata stays explicitly permitted.
func TestCandidateDeclassificationGuidanceStatesTheGateItDescribes(t *testing.T) {
	guidance := candidateDeclassificationGuidance()

	for _, want := range []string{
		strconv.Itoa(declassifyMinimumRun) + " or more characters",
		"contiguous span",
		"Paraphrase repository content in your own words",
		"including code comments",
		// What normalizeForDeclassify strips before measuring.
		"normalization",
		// What reversibleDecodings undoes before measuring.
		"base64", "hex", "escapes are decoded",
		// What sharedRun lets cross, named as allowed so the prompt is never
		// stricter than the host.
		"Always allowed",
		"symbol names", "file paths", "commit SHAs", "repository:// references",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("guidance missing %q:\n%s", want, guidance)
		}
	}

	if again := candidateDeclassificationGuidance(); again != guidance {
		t.Fatalf("guidance is not deterministic:\n--- first ---\n%s\n--- again ---\n%s", guidance, again)
	}
}

// The rule rides ONLY the runs whose text can become the candidate. A
// department reviewer judges completed work against a plan; the adversarial
// reviewer and the adjudicator receive the sanitized bundle; none of them was
// shown organizational source, and handing them an egress rule would imply
// they had been. Luna's closure and the CEO plan carry nothing at all.
func TestTheDeclassificationRuleRidesOnlyWorkerRuns(t *testing.T) {
	guidance := candidateDeclassificationGuidance()
	for _, purpose := range []ExecutionPurpose{
		PurposeCEOPlan, PurposeDepartmentPlan, PurposeDepartmentReview,
		PurposeCEOClosure, PurposeAdversarialReview, PurposeDesignAdjudication,
		PurposeImplementationPlan,
	} {
		if got := executionContractFor(purpose, nil); strings.Contains(got, guidance) {
			t.Errorf("executionContractFor(%q) carries the worker egress rule:\n%s", purpose, got)
		}
	}
	if got := executionContractFor(PurposeDepartmentReview, []EvidenceRequirement{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	}); strings.Contains(got, guidance) {
		t.Errorf("a reviewer with obligations still must not carry the egress rule:\n%s", got)
	}
}

// The failure shape that killed R13, kept impossible at the gate itself: the
// goal asked what MaxDesignRounds's comment says, the architect answered by
// quoting its prose, and the shared run crossed the threshold. A quote whose
// shared run reaches exactly the minimum must still be refused, one character
// less must cross freely -- the gate is untouched by this repair, and this
// pins that it stays so -- and the same semantics paraphrased passes with the
// citation that grounds it.
func TestTheSpanThatKilledR13IsStillRefusedAndItsParaphraseCrosses(t *testing.T) {
	comment := "// MaxDesignRounds bounds how many times a design may be sent back for revision before the run stops."
	prose := strings.TrimPrefix(normalizeForDeclassify(comment), "// ")

	exact := prose[:declassifyMinimumRun]
	err := DeclassifyCandidate("As the comment says: "+exact, evidenceOf(comment))
	if !errors.Is(err, ErrCandidateContaminated) {
		t.Fatalf("a threshold-exact quote must not reach a reviewer, got %v", err)
	}
	if err := DeclassifyCandidate("As the comment says: "+prose[:declassifyMinimumRun-1], evidenceOf(comment)); err != nil {
		t.Fatalf("one character below the threshold must cross: %v", err)
	}

	err = DeclassifyCandidate(
		"Its comment says it bounds how many times a design may be sent back for revision before the run stops.",
		evidenceOf(comment))
	if !errors.Is(err, ErrCandidateContaminated) {
		t.Fatalf("the verbatim quote must not reach a reviewer, got %v", err)
	}

	err = DeclassifyCandidate(
		"MaxDesignRounds caps how often one design may bounce between revision rounds before the whole run halts; see "+realCite+".",
		evidenceOf(comment))
	if err != nil {
		t.Fatalf("the same semantics paraphrased must cross: %v", err)
	}
}

// THE R13 CRITERION, end to end: the worker that will produce candidate text
// sees the egress rule in its ExecutionContract BEFORE answering -- derived
// from the gate's own threshold, never the durable instructions, never what
// retrieval searches for -- and no other executed purpose in the campaign,
// reviewer and adjudicator included, is handed a rule about source they were
// never shown.
func TestWorkerRunCarriesTheEgressRuleBeforeAnswering(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `"],` +
			`"evidence":[{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"}]}`

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
	guidance := candidateDeclassificationGuidance()
	if !strings.Contains(command.ExecutionContract, guidance) {
		t.Errorf("worker ExecutionContract missing the egress rule:\n%s", command.ExecutionContract)
	}
	if !strings.Contains(command.ExecutionContract, strconv.Itoa(declassifyMinimumRun)+" or more characters") {
		t.Errorf("worker contract does not state the gate's own threshold:\n%s", command.ExecutionContract)
	}
	// The evidence slots ride the same command but must not swallow the rule:
	// both contracts reach the model in one execution-time channel.
	if !strings.Contains(command.ExecutionContract, `subject="MaxDesignRounds"`) {
		t.Errorf("worker lost its evidence guidance when the egress rule was added:\n%s", command.ExecutionContract)
	}

	// Every OTHER purpose executed in this campaign -- department plan,
	// review, adversarial reviewer (Grok), adjudication (Luna), closure --
	// carries no egress rule: none of them was shown organizational source.
	for _, purpose := range fixture.purposes() {
		if purpose == PurposeDepartmentWorker {
			continue
		}
		other, ok := fixture.commandFor(purpose)
		if !ok {
			continue
		}
		if strings.Contains(other.ExecutionContract, guidance) {
			t.Errorf("%s received the worker egress rule though it never saw source:\n%s", purpose, other.ExecutionContract)
		}
	}

	// The rule is execution-time communication, not durable instruction.
	task, ok := designWorkerTask(t, fixture)
	if !ok {
		t.Fatal("no department worker task exists")
	}
	if strings.Contains(task.Instructions, "Egress rule") || strings.Contains(task.Instructions, "Paraphrase repository content") {
		t.Fatal("the egress rule was baked into durable instructions")
	}

	// And retrieval stayed exactly what it would have been without it: the
	// query searches for the obligation's subject, not for the rule's prose,
	// and the seeded subjects are the obligations' subjects alone.
	ctxPort := fixture.harness.contexts
	if ctxPort == nil {
		t.Fatal("fixture did not record context requests")
	}
	request, recorded := ctxPort.requests[command.Context.ID]
	if !recorded {
		t.Fatalf("no context request recorded for snapshot %d", command.Context.ID)
	}
	if strings.Contains(request.RepositoryQuery, "Paraphrase") || strings.Contains(request.RepositoryQuery, "Egress") {
		t.Fatal("the egress rule changed what retrieval searched for")
	}
	subjects := ctxPort.subjectsFor(command.Context.ID)
	if len(subjects) != 1 || subjects[0] != "MaxDesignRounds" {
		t.Fatalf("retrieval subjects drifted: %v", subjects)
	}
}
