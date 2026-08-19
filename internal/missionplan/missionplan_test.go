package missionplan

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
)

const baseSHA = "62961162de44a86c0e504daa976b01269c6fd097"

// unifiedDiff builds a real unified diff. CodeRunner's own parser rejects
// anything that does not touch a recognizable path, which is what caught these
// fixtures when they were placeholder strings.
func unifiedDiff(path string) string {
	return "--- a/" + path + "\n+++ b/" + path + "\n@@ -0,0 +1 @@\n+content\n"
}

func docsRequest() Request {
	return Request{
		TaskID: 42, BaseSHA: baseSHA, Scope: ScopeDocumentation,
		Objective: "Record the autonomous cycle evidence.",
		Changes: []Change{{
			Path:   "docs/implementation/autonomy-smoke/AUTONOMY_SMOKE.md",
			Intent: "Write the evidence file.",
			Patch:  unifiedDiff("docs/implementation/autonomy-smoke/AUTONOMY_SMOKE.md"),
		}},
		AcceptanceCriteria: []string{"Exactly one file changes"},
	}
}

func TestDeriveProducesAGovernedMission(t *testing.T) {
	derived, err := Derive(docsRequest())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if derived.Policy.BaseSHA != baseSHA {
		t.Fatalf("base sha=%q", derived.Policy.BaseSHA)
	}
	if len(derived.Policy.AllowedPaths) != 1 ||
		derived.Policy.AllowedPaths[0] != "docs/implementation/autonomy-smoke/AUTONOMY_SMOKE.md" {
		t.Fatalf("allowed paths=%v", derived.Policy.AllowedPaths)
	}
	// The gates are the host's, in full, always.
	if len(derived.Policy.RequiredGates) != 3 {
		t.Fatalf("gates=%v", derived.Policy.RequiredGates)
	}
	types := map[engineeringmission.GateType]bool{}
	for _, gate := range derived.Policy.RequiredGates {
		types[gate.Type] = true
	}
	for _, required := range []engineeringmission.GateType{engineeringmission.GateBuild, engineeringmission.GateVet, engineeringmission.GateTest} {
		if !types[required] {
			t.Fatalf("missing gate %s", required)
		}
	}
	// The generated plan is real, and CodeRunner accepts it. The previous
	// seam shipped a mission whose plan its own worker could not parse.
	if derived.Plan.SchemaVersion != coderunner.SchemaVersion {
		t.Fatalf("plan schema=%q", derived.Plan.SchemaVersion)
	}
	encoded, err := EncodePlan(derived.Plan)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := coderunner.ParsePlan(encoded)
	if err != nil {
		t.Fatalf("CodeRunner rejected the generated plan: %v", err)
	}
	last := parsed.Operations[len(parsed.Operations)-3:]
	if last[0].Type != coderunner.GoBuild || last[1].Type != coderunner.GoVet || last[2].Type != coderunner.GoTest {
		t.Fatalf("gates are not the final operations: %+v", parsed.Operations)
	}
	if parsed.Operations[0].Type != coderunner.ApplyPatch {
		t.Fatalf("first operation=%s", parsed.Operations[0].Type)
	}
	// No gofmt for a docs-only mission.
	for _, operation := range parsed.Operations {
		if operation.Type == coderunner.Gofmt {
			t.Fatal("gofmt was scheduled for a documentation-only mission")
		}
	}
}

func TestGoChangesScheduleGofmtAfterEveryPatch(t *testing.T) {
	request := docsRequest()
	request.Scope = ScopeInternalCode
	request.Changes = []Change{
		{Path: "internal/a/a.go", Intent: "x", Patch: unifiedDiff("internal/a/a.go")},
		{Path: "internal/b/b.go", Intent: "y", Patch: unifiedDiff("internal/b/b.go")},
	}
	derived, err := Derive(request)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	gofmtIndex, lastPatch := -1, -1
	for i, operation := range derived.Plan.Operations {
		switch operation.Type {
		case coderunner.Gofmt:
			gofmtIndex = i
		case coderunner.ApplyPatch:
			lastPatch = i
		}
	}
	if gofmtIndex < 0 {
		t.Fatal("go changes did not schedule gofmt")
	}
	if gofmtIndex < lastPatch {
		t.Fatal("gofmt runs against a half-applied tree")
	}
}

// The denylist is the part that must hold even when the plan is confident.
func TestStructurallyForbiddenPathsAreRefusedUnderEveryScope(t *testing.T) {
	forbidden := []string{
		".git/config",
		".github/workflows/ci.yml",
		"secrets/xai.token",
		"docs/canonical/role-catalog.yaml",
		"docs/canonical/capability-matrix.yaml",
		"migrations/000050_x.up.sql",
		"scripts/check-model-runtime-fitness.sh",
		"deployments/prod.yaml",
		"config/repositories.yaml",
		"go.mod",
		"go.sum",
		"compose.yaml",
		".env",
		"Makefile",
		"AGENT.md",
		"/etc/passwd",
		"../outside.go",
		"internal/../../escape.go",
	}
	for _, scope := range []Scope{ScopeDocumentation, ScopeInternalCode} {
		for _, target := range forbidden {
			request := docsRequest()
			request.Scope = scope
			request.Changes = []Change{{Path: target, Intent: "x", Patch: unifiedDiff("docs/implementation/x.md")}}
			if _, err := Derive(request); err == nil {
				t.Fatalf("scope %s accepted forbidden path %q", scope, target)
			}
		}
	}
}

// Scope is a boundary, not a hint: documentation missions cannot reach code.
func TestScopeConfinesTheMission(t *testing.T) {
	request := docsRequest()
	request.Changes = []Change{{Path: "internal/executive/orchestrator.go", Intent: "x", Patch: unifiedDiff("internal/executive/orchestrator.go")}}
	if _, err := Derive(request); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("a documentation mission reached into code: %v", err)
	}
	// And the same path is fine under the code scope.
	request.Scope = ScopeInternalCode
	if _, err := Derive(request); err != nil {
		t.Fatalf("code scope rejected internal code: %v", err)
	}
	// An unknown scope never derives anything.
	request.Scope = Scope("anything")
	if _, err := Derive(request); err == nil {
		t.Fatal("an unknown scope produced a mission")
	}
}

// AllowedPaths is exactly the set the plan declared -- never widened, and
// never inherited from a directory.
func TestAllowedPathsAreExactlyTheDeclaredFiles(t *testing.T) {
	request := docsRequest()
	request.Scope = ScopeInternalCode
	request.Changes = []Change{
		{Path: "internal/b/b.go", Intent: "x", Patch: unifiedDiff("internal/b/b.go")},
		{Path: "internal/a/a.go", Intent: "y", Patch: unifiedDiff("internal/a/a.go")},
	}
	derived, err := Derive(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(derived.Policy.AllowedPaths, ",") != "internal/a/a.go,internal/b/b.go" {
		t.Fatalf("allowed=%v -- must be exactly the declared files, sorted", derived.Policy.AllowedPaths)
	}
	if engineeringmission.PathAllowed(derived.Policy.AllowedPaths, "internal/a/other.go") {
		t.Fatal("a sibling file was allowed by association")
	}
	if engineeringmission.PathAllowed(derived.Policy.AllowedPaths, "internal/executive/orchestrator.go") {
		t.Fatal("an undeclared file was allowed")
	}
}

// BaseSHA must be an exact commit. Anything symbolic makes the mission's base
// whatever the repository happened to be.
func TestBaseSHAMustBeAnExactCommit(t *testing.T) {
	for _, value := range []string{"", "HEAD", "main", "latest", "6296116", strings.ToUpper(baseSHA), baseSHA + "0"} {
		request := docsRequest()
		request.BaseSHA = value
		if _, err := Derive(request); err == nil {
			t.Fatalf("base sha %q was accepted", value)
		}
	}
}

func TestDeriveIsDeterministic(t *testing.T) {
	first, err := Derive(docsRequest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Derive(docsRequest())
	if err != nil {
		t.Fatal(err)
	}
	firstBody, _, err := first.Policy.MarshalEvidence()
	if err != nil {
		t.Fatal(err)
	}
	secondBody, _, err := second.Policy.MarshalEvidence()
	if err != nil {
		t.Fatal(err)
	}
	if len(firstBody) != len(secondBody) {
		t.Fatal("policy evidence is not deterministic")
	}
	a, _ := EncodePlan(first.Plan)
	b, _ := EncodePlan(second.Plan)
	if string(a) != string(b) {
		t.Fatal("generated plan is not deterministic")
	}
}

func TestMalformedPlansAreRefused(t *testing.T) {
	cases := map[string]func(*Request){
		"no changes":       func(r *Request) { r.Changes = nil },
		"empty patch":      func(r *Request) { r.Changes[0].Patch = "  " },
		"no objective":     func(r *Request) { r.Objective = "" },
		"no criteria":      func(r *Request) { r.AcceptanceCriteria = nil },
		"negative task id": func(r *Request) { r.TaskID = -1 },
		"duplicate path":   func(r *Request) { r.Changes = append(r.Changes, r.Changes[0]) },
		"too many changes": func(r *Request) {
			for i := 0; i < maxChangesPerMission+1; i++ {
				r.Changes = append(r.Changes, Change{Path: "docs/implementation/x" + strings.Repeat("y", i) + ".md", Intent: "i", Patch: unifiedDiff("docs/implementation/x.md")})
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := docsRequest()
			mutate(&request)
			if _, err := Derive(request); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}
