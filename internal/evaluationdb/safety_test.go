package evaluationdb

import (
	"context"
	"testing"
)

func TestRequireDisposableRejectsNilStore(t *testing.T) {
	if err := RequireDisposable(context.Background(), nil); err == nil {
		t.Fatal("nil store was accepted")
	}
}
