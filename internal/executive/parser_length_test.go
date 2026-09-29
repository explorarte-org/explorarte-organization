package executive

import (
	"errors"
	"strings"
	"testing"
)

// Local smokes #32 to #34: a retry told only "9022 UTF-8 bytes exceeds maximum 8000" came back at
// 10486. The refusal now says how much to cut, toward what target, and that bytes are counted.
func TestAnOverLengthRefusalSaysHowMuchToCut(t *testing.T) {
	err := validateRequiredString(strings.Repeat("a", 9022), 8000, "summary")
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"9022 UTF-8 bytes exceeds maximum 8000", "cut at least 2222 bytes", "aim for about 6800", "not characters"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not say %q: %v", want, err)
		}
	}
}
