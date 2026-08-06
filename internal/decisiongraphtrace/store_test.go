package decisiongraphtrace

import (
	"testing"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

func TestNewRejectsInvalidInputs(t *testing.T) {
	if _, err := New(nil, "org"); err == nil {
		t.Fatal("expected error for a nil store")
	}
	if _, err := New(&platformpostgres.Store{}, "org"); err == nil {
		t.Fatal("expected error for a store with no initialized pool")
	}
}
