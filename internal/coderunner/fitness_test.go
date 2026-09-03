package coderunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFitnessIsAHostFixedCheck(t *testing.T) {
	if Fitness.Mutates() {
		t.Fatal("FITNESS must not be classified as a workspace mutation")
	}
	if !Fitness.isCheck() {
		t.Fatal("FITNESS must be recorded as a verification check")
	}

	root := t.TempDir()
	makefile := ".PHONY: test-kernel-governance-fitness test-executive-fitness\n" +
		"test-kernel-governance-fitness:\n\t@printf kernel\n" +
		"test-executive-fitness:\n\t@printf executive\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(makefile), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (&Executor{Workspace: root, OperationTimeout: time.Minute}).ExecuteOperation(context.Background(), Operation{Type: Fitness})
	if err != nil {
		t.Fatalf("fixed FITNESS operation failed: %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, "kernelexecutive") {
		t.Fatalf("FITNESS did not run both fixed gates: %+v", result)
	}
}

func TestFitnessRejectsPlanConfiguration(t *testing.T) {
	plan := Plan{SchemaVersion: SchemaVersion, Operations: []Operation{{Type: Fitness, Packages: []string{"./..."}}}}
	if err := plan.Validate(); err == nil {
		t.Fatal("FITNESS accepted model-configurable package arguments")
	}
}
