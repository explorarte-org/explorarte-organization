package postgres

import (
	"bytes"
	"testing"
)

func TestCanonicalStoredInvocationJSONRestoresCanonicalBytes(t *testing.T) {
	got, err := canonicalStoredInvocationJSON(
		[]byte(`{ "z": 3, "a": { "y": 2, "x": 1 } }`),
	)
	if err != nil {
		t.Fatalf("canonicalize stored JSON: %v", err)
	}

	want := []byte(`{"a":{"x":1,"y":2},"z":3}`)
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical JSON mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestCanonicalStoredInvocationJSONPreservesNullAsNoResult(t *testing.T) {
	got, err := canonicalStoredInvocationJSON([]byte(`null`))
	if err != nil {
		t.Fatalf("canonicalize stored null: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil JSON result for stored null, got %q", got)
	}
}
