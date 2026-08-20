package runtimeadapter

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The coordination hold is one durable fact that two packages must agree on.
// internal/tasks owns the reason code; internal/executive has to recognise it
// without importing the Tasks Engine, because this adapter is that boundary by
// design. So the executive side restates the string, and a restatement is only
// safe if something fails when the two stop matching.
//
// Without this, a rename in internal/tasks would leave the executive fake
// happily green while production stopped recognising held tasks at all --
// every child would look already-published and would never be released.
func TestCoordinationHoldReasonCodeIsOneContract(t *testing.T) {
	body, err := os.ReadFile("../test_fakes_test.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "const coordinationHoldReasonCode = " + strconv.Quote(tasks.ReasonCodeCoordinationHold)
	if !strings.Contains(string(body), want) {
		t.Fatalf("internal/executive restates the coordination hold reason code and no longer agrees with internal/tasks (%q); both must change together, and durable rows already carry the old value", tasks.ReasonCodeCoordinationHold)
	}
}
