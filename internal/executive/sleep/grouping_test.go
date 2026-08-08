package sleep

import (
	"math"
	"testing"
	"time"
)

func TestAnalyzeGroupThresholds(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	makeGroup := func(successes, failures int) Group {
		g := Group{Key: GroupKey{UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa", ProviderID: "deepseek"}}
		for i := 0; i < successes; i++ {
			g.Experiences = append(g.Experiences, testExperience(int64(i+1), VerificationVerified, "deepseek", now.Add(time.Duration(i)*time.Minute)))
		}
		for i := 0; i < failures; i++ {
			g.Experiences = append(g.Experiences, testExperience(int64(successes+i+1), VerificationContradicted, "deepseek", now.Add(time.Duration(successes+i)*time.Minute)))
		}
		return g
	}

	exactForty := AnalyzeGroup(makeGroup(2, 3), 3)
	if exactForty.PassRate != 0.4 || exactForty.Eligible || exactForty.Contradiction {
		t.Fatalf("0.40 boundary=%+v", exactForty)
	}
	mixed := AnalyzeGroup(makeGroup(3, 2), 3)
	if mixed.PassRate != 0.6 || !mixed.Eligible || !mixed.Contradiction {
		t.Fatalf("mixed=%+v", mixed)
	}
	strong := AnalyzeGroup(makeGroup(17, 3), 3)
	if strong.PassRate != 0.85 || !strong.Eligible || strong.Contradiction {
		t.Fatalf("0.85 boundary=%+v", strong)
	}
}

func TestAnalyzeGroupTreatsVerifiedAndInferredAsSuccess(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	group := Group{Key: GroupKey{UnitID: "marketing", RoleID: "marketing/analista_performance", ProviderID: "deepseek"}, Experiences: []Experience{
		testExperience(1, VerificationVerified, "deepseek", now),
		testExperience(2, VerificationInferred, "deepseek", now.Add(time.Minute)),
		testExperience(3, VerificationUnknown, "deepseek", now.Add(2*time.Minute)),
		testExperience(4, VerificationContradicted, "deepseek", now.Add(3*time.Minute)),
	}}
	analysis := AnalyzeGroup(group, 3)
	if analysis.SuccessCount != 2 || analysis.FailureCount != 2 || analysis.PassRate != 0.5 || !analysis.Contradiction {
		t.Fatalf("analysis=%+v", analysis)
	}
}

func TestConfidenceFormula(t *testing.T) {
	got := Confidence(4, 8, 0.75, 2, true)
	want := 0.2625 // (4/8)*.75*1.1 - .15
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("confidence=%f want=%f", got, want)
	}
	got = Confidence(8, 8, 0.875, 2, false)
	want = 0.9625 // 1*.875*1.1
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("strong confidence=%f want=%f", got, want)
	}
	if got := Confidence(100, 8, 1, 4, false); got != 1 {
		t.Fatalf("clamp=%f want=1", got)
	}
}

func TestPortabilityClassification(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	group := func(provider string, labels ...string) Group {
		g := Group{Key: GroupKey{UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa", ProviderID: provider}}
		for i, label := range labels {
			g.Experiences = append(g.Experiences, testExperience(int64(len(provider)*100+i+1), label, provider, now.Add(time.Duration(i)*time.Minute)))
		}
		return g
	}
	primary := group("provider-a", VerificationVerified, VerificationVerified, VerificationVerified)
	consistent := group("provider-b", VerificationVerified, VerificationVerified, VerificationVerified)
	portability, evidence := PortabilityFor(primary, []Group{primary, consistent}, 3)
	if portability.ProvidersSeen != 2 || portability.Classification != "consistent_eligibility_band_across_providers" || len(evidence) != 6 {
		t.Fatalf("consistent portability=%+v evidence=%d", portability, len(evidence))
	}
	weak := group("provider-c", VerificationContradicted, VerificationContradicted, VerificationVerified)
	portability, _ = PortabilityFor(primary, []Group{primary, weak}, 3)
	if portability.Classification != "provider_dependent" {
		t.Fatalf("dependent portability=%+v", portability)
	}
}

func testExperience(runID int64, label, provider string, observedAt time.Time) Experience {
	return Experience{
		RunID: runID, TaskID: runID + 1000, AttemptID: runID + 2000,
		UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa", ProviderID: provider, ProviderModelID: provider + "-model",
		VerificationLabel: label, EvidenceDigest: sha256Hex("evidence:" + provider + ":" + observedAt.String()), ObservedAt: observedAt,
	}
}
