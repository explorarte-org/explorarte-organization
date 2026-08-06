package cellworker

import (
	"math/rand"
	"testing"
	"time"
)

func TestBackoffStaysWithinBoundsAndCapsAtMax(t *testing.T) {
	min, max := time.Millisecond, 100*time.Millisecond
	b := newBackoff(min, max, rand.New(rand.NewSource(1)))

	prevCurrent := min
	for i := 0; i < 50; i++ {
		d := b.Next()
		if d < 0 || d > max {
			t.Fatalf("iteration %d: backoff %s outside [0, %s]", i, d, max)
		}
		if d > prevCurrent {
			t.Fatalf("iteration %d: backoff %s exceeded the pre-jitter ceiling %s", i, d, prevCurrent)
		}
		prevCurrent *= 2
		if prevCurrent > max || prevCurrent <= 0 {
			prevCurrent = max
		}
	}
}

func TestBackoffResetReturnsToMin(t *testing.T) {
	min, max := 2*time.Millisecond, 64*time.Millisecond
	b := newBackoff(min, max, rand.New(rand.NewSource(2)))

	for i := 0; i < 10; i++ {
		b.Next()
	}
	b.Reset()

	d := b.Next()
	if d < 0 || d > min {
		t.Fatalf("expected first backoff after Reset to be within [0, %s], got %s", min, d)
	}
}

func TestBackoffZeroMinNeverPanics(t *testing.T) {
	b := newBackoff(0, time.Millisecond, rand.New(rand.NewSource(3)))
	for i := 0; i < 5; i++ {
		if d := b.Next(); d != 0 {
			t.Fatalf("expected 0 backoff from a 0 minimum before it ever grows, got %s", d)
		}
	}
}
