package staging

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateTargetRef(t *testing.T) {
	if err := ValidateTargetRef("refs/heads/integration"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"main", "refs/tags/v1", "refs/heads/main~1", "refs/heads/a..b", "refs/heads/x@{1}", "refs/heads/a b"} {
		if err := ValidateTargetRef(value); err == nil {
			t.Fatalf("ValidateTargetRef(%q) accepted", value)
		}
	}
}

func TestWorkspaceStateMachineTerminality(t *testing.T) {
	if err := ValidateWorkspaceTransition(WorkspaceProvisioning, WorkspaceActive); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspaceTransition(WorkspaceSealed, WorkspaceActive); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v", err)
	}
	if err := ValidateWorkspaceTransition(WorkspaceCleaned, WorkspaceActive); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v", err)
	}
}

func TestPromotionStateMachineNoReopen(t *testing.T) {
	if err := ValidatePromotionTransition(PromotionAwaitingGates, PromotionApproved); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePromotionTransition(PromotionApplied, PromotionApproved); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v", err)
	}
}

func TestValidateSeparateRootsRejectsOverlapAndSymlink(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one")
	two := filepath.Join(root, "two")
	if err := os.Mkdir(one, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(two, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSeparateRoots(one, two); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(one, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSeparateRoots(one, child); err == nil {
		t.Fatal("overlap accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(one, link); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalRoot(link); !errors.Is(err, ErrUnsafeRepository) {
		t.Fatalf("got %v", err)
	}
}
