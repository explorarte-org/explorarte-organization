package executive

import (
	"strings"
	"testing"
)

// Local smoke #28 (root 1647): both design workers blocked because their task asked for a diff of a
// test file the host never shows at design time. The host rule says a design is not a diff.
func TestADesignIsNotADiffAndAMissingTestFileIsNotABlocker(t *testing.T) {
	guidance := designDeliverableGuidance()
	for _, want := range []string{
		"A design states the change in words",
		"It is not a diff",
		"written after design freeze by the implementation plan, which is given the exact file",
		"you are not shown test files (*_test.go), and that is expected: it is never a reason to block",
		"this host rule takes precedence",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("the design guidance lacks %q", want)
		}
	}
}
