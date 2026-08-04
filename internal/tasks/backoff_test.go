package tasks

import (
	"testing"
	"time"
)

func TestRetryPolicyDelay(t *testing.T) {
	policy := RetryPolicy{BaseDelay: 5 * time.Second, MaxDelay: 10 * time.Minute}
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 5 * time.Second}, {1, 5 * time.Second}, {2, 10 * time.Second}, {3, 20 * time.Second},
		{4, 40 * time.Second}, {5, 80 * time.Second}, {8, 10 * time.Minute}, {100, 10 * time.Minute},
	}
	for _, test := range tests {
		if got := policy.Delay(test.attempt); got != test.want {
			t.Fatalf("Delay(%d) = %s, want %s", test.attempt, got, test.want)
		}
	}
}
