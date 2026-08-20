package executive

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The Executive's inability to reach Model Runtime's execute side is meant to
// be a property of the code, not a rule someone remembers. These tests are
// where that claim is actually checked.

func TestModelInvocationReaderExposesOnlyReads(t *testing.T) {
	readerType := reflect.TypeOf((*ModelInvocationReader)(nil)).Elem()
	// ProviderFailureRetryable asks Model Runtime a question and receives a
	// bool. It creates, claims, dispatches and cancels nothing, which is the
	// line this guard exists to hold. It is listed rather than the count
	// merely bumped, so adding one is always a deliberate act with a name
	// attached.
	allowed := map[string]bool{
		"GetInvocation": true, "FindTaskAttemptInvocations": true, "GetResult": true,
		"ProviderFailureRetryable": true,
	}
	if readerType.NumMethod() != len(allowed) {
		t.Fatalf("read side has %d methods; every addition here is a potential execute path", readerType.NumMethod())
	}
	for i := 0; i < readerType.NumMethod(); i++ {
		if name := readerType.Method(i).Name; !allowed[name] {
			t.Fatalf("unexpected method %q on the model read side", name)
		}
	}
}

func TestOrchestratorHoldsOnlyTheReadSideAndTheHarness(t *testing.T) {
	orchestratorType := reflect.TypeOf(Orchestrator{})
	models, ok := orchestratorType.FieldByName("models")
	if !ok || models.Type != reflect.TypeOf((*ModelInvocationReader)(nil)).Elem() {
		t.Fatalf("orchestrator model dependency is %v; it must be the read-only port", models.Type)
	}
	harness, ok := orchestratorType.FieldByName("harness")
	if !ok || harness.Type != reflect.TypeOf((*HarnessExecutor)(nil)).Elem() {
		t.Fatalf("orchestrator execute dependency is %v; it must be the harness port", harness.Type)
	}
	executorType := reflect.TypeOf((*HarnessExecutor)(nil)).Elem()
	if executorType.NumMethod() != 1 || executorType.Method(0).Name != "Execute" {
		t.Fatal("the harness port must expose exactly one execute verb")
	}
}

// TestProductiveExecutiveNeverNamesAnExecuteSideOperation is the structural
// proof. Interfaces alone would not catch a future file in this package that
// reached into Model Runtime directly, so the productive sources are scanned
// for the operations that create or dispatch provider work.
func TestProductiveExecutiveNeverNamesAnExecuteSideOperation(t *testing.T) {
	// The tokens are call shapes, not type names: holding
	// *modelruntime.InvocationService is fine and necessary for the read side
	// (Get/FindTaskAttempt/Result); what must not appear is any call that
	// creates or dispatches provider work.
	forbidden := []string{"EnsureInvocation", ".Create(", ".Dispatch("}
	for _, dir := range []string{".", "runtimeadapter", "bootstrap", "postrun", "sleep", "smoke"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			// runtimeadapter/harness.go legitimately builds the Harness runtime
			// and its model executor; that IS the sanctioned path into Model
			// Runtime, and it goes through the executionharness boundary rather
			// than around it.
			if path == filepath.Join("runtimeadapter", "harness.go") {
				continue
			}
			// bootstrap/missionprovisioning.go calls
			// engineeringmission.Service.Create, which creates a durable TASK and
			// touches no model runtime at all. The forbidden token list is a
			// substring heuristic aimed at Model Runtime's own Create /
			// EnsureInvocation; this file reaches models through nothing, and
			// exempting it by path is narrower than loosening the token for
			// every file in the package.
			if path == filepath.Join("bootstrap", "missionprovisioning.go") {
				continue
			}
			for _, token := range forbidden {
				if !strings.Contains(string(body), token) {
					continue
				}
				// A comment explaining the absence is not a call site.
				if onlyInComments(string(body), token) {
					continue
				}
				t.Fatalf("%s references %q: the productive executive must reach models only through the read port or the harness", path, token)
			}
		}
	}
}

func onlyInComments(body, token string) bool {
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, token) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "*") {
			return false
		}
	}
	return true
}
