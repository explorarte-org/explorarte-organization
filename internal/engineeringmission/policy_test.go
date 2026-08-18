package engineeringmission

import "testing"

func validPolicy() MissionPolicy {
	return MissionPolicy{TaskID: 1, BaseSHA: "0123456789012345678901234567890123456789", Objective: "change fixture", AllowedPaths: []string{"foo/bar"}, AcceptanceCriteria: []string{"tests pass"}, RequiredGates: []RequiredGate{{Type: GateTest, Packages: []string{"./foo/..."}}}}
}
func TestPolicyAndPathBoundary(t *testing.T) {
	p, err := validPolicy().Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if !PathAllowed(p.AllowedPaths, "foo/bar/x.go") || PathAllowed(p.AllowedPaths, "foo/bar-evil/x.go") {
		t.Fatal("path boundary")
	}
}
func TestPolicyRejectsUnsafeGate(t *testing.T) {
	p := validPolicy()
	p.RequiredGates[0].Packages = []string{"../x"}
	if _, err := p.Normalize(); err == nil {
		t.Fatal("expected rejection")
	}
}
