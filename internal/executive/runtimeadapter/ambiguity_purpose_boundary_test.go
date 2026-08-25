package runtimeadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// Boundary guards for the ambiguity reconciler's identity input (B2).
//
// The classifier matches the invocation's durable Purpose against a closed
// set of LegacyPurpose strings. That only works if the value PERSISTED is
// exactly the LegacyPurpose representation -- not the enum's Go string
// ("design-adjudication" vs "design_adjudication"), which would make every
// NEW ambiguity classify unknown while the frozen legacy format kept R14
// looking recovered. These two tests pin both halves of that wire.

// The Executive side: HarnessRunCommand.Purpose reaches the model executor
// config as the byte-exact LegacyPurpose string.
func TestExecutiveRunStatesItsLegacyPurposeInTheModelExecutorConfig(t *testing.T) {
	var captured modelruntimeadapter.Config
	h := Harness{
		OrganizationID: "explorarte",
		Authority:      &allowAuthority{},
		History:        newMemoryHistory(),
		NewModelExecutor: func(config modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			captured = config
			return nil, errors.New("captured config")
		},
		Clock: executive.ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	}
	command := testCommand()
	command.Purpose = executive.PurposeDesignAdjudication
	if _, err := h.Execute(context.Background(), command); err == nil {
		t.Fatal("expected the stub executor error to surface")
	}

	want := executive.PurposeDesignAdjudication.LegacyPurpose()
	if captured.Purpose != want {
		t.Fatalf("Config.Purpose = %q, want the LegacyPurpose representation %q", captured.Purpose, want)
	}
}
