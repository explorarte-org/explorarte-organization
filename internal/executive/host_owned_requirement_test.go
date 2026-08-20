package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func validateShape(t *testing.T, key, reqType string, required bool) error {
	t.Helper()
	return satisfiabilityValidator(t).ValidateDepartmentPlan(context.Background(), 7,
		"ingenieria_ia", "ingenieria_ia/orquestador", keyedRequirementPlan(key, reqType, required))
}

// 1: the exact host-owned shape is redundant but compatible.
func TestReservedKeyWithTheHostShapeIsAccepted(t *testing.T) {
	if err := validateShape(t, hostOwnedResultRequirementKey, "result", true); err != nil {
		t.Fatalf("the host-owned shape was rejected: %v", err)
	}
}

// 2, 3, 4: the key is reserved, and occupying it with any other shape is
// refused. This is worse than an unsatisfiable requirement: it would make
// appendResultRequirement decline to attach the real one, leaving the task
// with no blocking requirement and its model result nowhere to be recorded.
func TestReservedKeyWithAnyOtherShapeIsRefused(t *testing.T) {
	cases := map[string]struct {
		reqType  string
		required bool
	}{
		"result but optional": {"result", false},
		"artifact optional":   {"artifact", false},
		"condition optional":  {"condition", false},
		"artifact required":   {"artifact", true},
		"approval optional":   {"approval", false},
		"check optional":      {"check", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateShape(t, hostOwnedResultRequirementKey, tc.reqType, tc.required)
			if !errors.Is(err, ErrRequirementUnsatisfiable) {
				t.Fatalf("%s was accepted on the reserved key: %v", name, err)
			}
			if !strings.Contains(err.Error(), "reserved host-owned requirement") {
				t.Fatalf("the refusal does not identify the reserved key: %v", err)
			}
		})
	}
}

// 5: an optional requirement under any other key stays legal.
func TestOptionalCustomKeysRemainAllowed(t *testing.T) {
	for _, reqType := range []string{"result", "artifact", "check", "approval", "condition"} {
		t.Run(reqType, func(t *testing.T) {
			if err := validateShape(t, "custom_result", reqType, false); err != nil {
				t.Fatalf("optional custom %s was rejected: %v", reqType, err)
			}
		})
	}
}

// 6: a blocking requirement under any other key is still refused.
func TestBlockingCustomKeysRemainRefused(t *testing.T) {
	err := validateShape(t, "document_content_result", "result", true)
	if !errors.Is(err, ErrRequirementUnsatisfiable) {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "document_content_result") {
		t.Fatalf("the refusal does not name the key: %v", err)
	}
}

// 7: a proposal without the reserved key gets exactly the host's shape.
func TestHostAttachesItsRequirementWhenAbsent(t *testing.T) {
	out := appendResultRequirement([]RequirementProposal{
		{Key: "notes", Type: "condition", Description: "descriptive", Required: false},
	})
	if len(out) != 2 {
		t.Fatalf("requirements=%d, want the original plus the host's", len(out))
	}
	attached := out[len(out)-1]
	if !isHostOwnedWorkerRequirement(attached) {
		t.Fatalf("the host attached the wrong shape: %+v", attached)
	}
	if attached.Key != hostOwnedResultRequirementKey || attached.Type != "result" || !attached.Required {
		t.Fatalf("attached=%+v", attached)
	}
}

// 8: the exact host shape already present is not duplicated.
func TestHostDoesNotDuplicateItsOwnRequirement(t *testing.T) {
	existing := RequirementProposal{Key: hostOwnedResultRequirementKey, Type: "result", Description: "already here", Required: true}
	out := appendResultRequirement([]RequirementProposal{existing})
	if len(out) != 1 {
		t.Fatalf("requirements=%d, want no duplicate: %+v", len(out), out)
	}
}

// The regression this commit exists for: the reserved key occupied by a
// non-host shape must never reach appendResultRequirement, but if it somehow
// did, the host would still attach its own rather than silently leaving the
// task with no blocking requirement.
func TestHostAttachesItsRequirementDespiteAnImpostorUnderTheReservedKey(t *testing.T) {
	impostor := RequirementProposal{Key: hostOwnedResultRequirementKey, Type: "result", Description: "optional impostor", Required: false}
	out := appendResultRequirement([]RequirementProposal{impostor})

	found := false
	for _, requirement := range out {
		if isHostOwnedWorkerRequirement(requirement) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no host-owned requirement survived: %+v", out)
	}
	// And resultRequirementID -- which recordHarnessSuccess depends on -- can
	// therefore find one.
	records := make([]RequirementRecord, 0, len(out))
	for i, requirement := range out {
		records = append(records, RequirementRecord{ID: int64(i + 1), Key: requirement.Key, Type: requirement.Type, Required: requirement.Required})
	}
	if resultRequirementID(records) == 0 {
		t.Fatal("resultRequirementID found nothing: a validated model result would have nowhere to be recorded")
	}
}
