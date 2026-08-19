package engineeringmission

import "testing"
import (
	"context"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"strings"
)

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

type reviewFake struct {
	p    staging.Promotion
	w    staging.Workspace
	last staging.SubmitReviewCommand
}

func (f *reviewFake) GetWorkspace(context.Context, int64) (staging.Workspace, error) { return f.w, nil }
func (f *reviewFake) GetPromotion(context.Context, int64) (staging.Promotion, error) { return f.p, nil }
func (*reviewFake) RecordCheck(context.Context, staging.RecordCheckCommand) (staging.Check, error) {
	return staging.Check{}, nil
}
func (*reviewFake) RequestPromotion(context.Context, staging.RequestPromotionCommand) (staging.Promotion, error) {
	return staging.Promotion{}, nil
}
func (f *reviewFake) SubmitReview(_ context.Context, c staging.SubmitReviewCommand) (staging.Promotion, error) {
	f.last = c
	return f.p, nil
}
func TestReviewMissionBlocksSelfReviewAndEncodesVerdicts(t *testing.T) {
	f := &reviewFake{p: staging.Promotion{ID: 2, TaskID: 1, WorkspaceID: 3}, w: staging.Workspace{ID: 3, ActorRoleID: "author"}}
	s := Service{Promotion: f}
	if _, err := s.ReviewMission(context.Background(), 2, 4, "author", Approve, "ok", "reason"); err == nil {
		t.Fatal("self review accepted")
	}
	if _, err := s.ReviewMission(context.Background(), 2, 4, "reviewer", Remediate, "missing_test", "needs test"); err != nil {
		t.Fatal(err)
	}
	if f.last.Decision != staging.ReviewReject || !strings.Contains(f.last.Reference, "/remediate/missing_test") {
		t.Fatalf("bad remediation encoding: %+v", f.last)
	}
	if _, err := s.ReviewMission(context.Background(), 2, 4, "reviewer", Block, "unsafe", "blocked"); err != nil {
		t.Fatal(err)
	}
	if f.last.Decision != staging.ReviewReject || !strings.Contains(f.last.Reference, "/block/unsafe") {
		t.Fatalf("bad block encoding: %+v", f.last)
	}
}
