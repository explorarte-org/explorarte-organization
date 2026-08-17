package coderunner

import "testing"

func TestPlanRejectsShellAndUnsafePaths(t *testing.T) {
	for _, p := range []Plan{{SchemaVersion: SchemaVersion, Operations: []Operation{{Type: "RUN"}}}, {SchemaVersion: SchemaVersion, Operations: []Operation{{Type: ReadFile, Path: "/etc/passwd"}}}, {SchemaVersion: SchemaVersion, Operations: []Operation{{Type: ReadFile, Path: "../x"}}}} {
		if err := p.Validate(); err == nil {
			t.Fatal("accepted unsafe plan")
		}
	}
}
func TestPlanAcceptsTypedOperations(t *testing.T) {
	p, err := ParsePlan([]byte(`{"schema_version":"code-runner-execution/v1","operations":[{"type":"GO_TEST","packages":["./internal/coderunner"]}]}`))
	if err != nil || len(p.Operations) != 1 {
		t.Fatal(err)
	}
}
func TestPlanRejectsUnsafePackageBeforeExecution(t *testing.T) {
	for _, pkg := range []string{"../...", "/tmp/x", "./x;rm", "-run=Test"} {
		p := Plan{SchemaVersion: SchemaVersion, Operations: []Operation{{Type: GoTest, Packages: []string{pkg}}}}
		if err := p.Validate(); err == nil {
			t.Fatalf("accepted %q", pkg)
		}
	}
}
