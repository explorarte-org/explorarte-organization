package buildinfo

import "testing"

func TestInfoString(t *testing.T) {
	info := Info{Version: "v1.2.3", Commit: "abc123", BuildTime: "2026-08-04T00:00:00Z"}
	got := info.String()
	want := "explorarte-organization version=v1.2.3 commit=abc123 build_time=2026-08-04T00:00:00Z"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
