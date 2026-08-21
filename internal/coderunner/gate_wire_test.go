package coderunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// operationFailure being correct is worth nothing if Execute does not call
// it. That is the half that was broken here and in two other places today:
// the function was never the problem, the wiring was.
//
// So this drives a real failing operation and reads what actually comes back.
func TestExecuteReportsWhatTheFailingCommandSaid(t *testing.T) {
	root := t.TempDir()
	// A package that cannot compile. GO_BUILD will exit non-zero and say
	// exactly why, which is the output that used to be discarded.
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module gatewiring\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"),
		[]byte("package gatewiring\n\nfunc Broken() { return undefinedSymbol }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor := &Executor{Workspace: root, MaxOutput: 1 << 20, OperationTimeout: 2 * time.Minute}
	results, err := executor.Execute(context.Background(), Plan{
		SchemaVersion: SchemaVersion,
		Operations:    []Operation{{Type: GoBuild}},
	})
	if err == nil {
		t.Fatal("a package that does not compile must fail the gate")
	}
	message := err.Error()
	if !strings.Contains(message, "GO_BUILD") {
		t.Errorf("the failure must name the operation: %v", err)
	}
	if !strings.Contains(message, "exit code") {
		t.Errorf("the failure must carry the exit code: %v", err)
	}
	// The compiler's own words. Without them "operation GO_BUILD failed" is
	// consistent with a broken package, a missing toolchain, an unwritable
	// cache and a full disk, and distinguishes none of them.
	if !strings.Contains(message, "undefinedSymbol") {
		t.Errorf("the failure must carry what the compiler said: %v", err)
	}
	if len(results) != 1 || results[0].Success {
		t.Fatalf("the failing result must still be returned: %+v", results)
	}
}
