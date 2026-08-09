package identifiers

import (
	"reflect"
	"testing"
)

func TestExtractDigitRunsCoreCases(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"hyphenated identifier", "agent hit error-20 during dispatch", []string{"20"}},
		{"space-separated identifier", "agent hit error 20 during dispatch", []string{"20"}},
		{"distinct larger number is not the same identifier", "agent hit error 2000 during dispatch", []string{"2000"}},
		{"no digits", "no numbers here", []string{}},
		{"multiple runs preserve order and duplicates", "code 12 then code 34 then code 12 again", []string{"12", "34", "12"}},
		{"leading zeros are preserved literally, not numerically normalized", "ticket 007 vs ticket 7", []string{"007", "7"}},
		{"empty input", "", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractDigitRuns(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ExtractDigitRuns(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestExtractDigitRunsNeverConflatesDifferentNumbers guards the exact
// three-way scenario this package exists for: "error 20" must never match
// "error 2000" through this channel, no matter how the two get compared
// downstream (set overlap, equality, etc.) — they must simply never share
// an element.
func TestExtractDigitRunsNeverConflatesDifferentNumbers(t *testing.T) {
	hyphenated := ExtractDigitRuns("error-20")
	spaced := ExtractDigitRuns("error 20")
	larger := ExtractDigitRuns("error 2000")

	if !reflect.DeepEqual(hyphenated, spaced) {
		t.Fatalf("hyphenated=%v spaced=%v want equal (same identifier)", hyphenated, spaced)
	}
	for _, small := range spaced {
		for _, big := range larger {
			if small == big {
				t.Fatalf("%q incorrectly equals %q", small, big)
			}
		}
	}
}
