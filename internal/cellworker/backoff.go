package cellworker

import (
	"math/rand"
	"time"
)

// backoff implements full-jitter exponential backoff (AWS-style): each call
// to Next returns a random duration in [0, current], then doubles current
// toward max. Reset returns it to min. Not safe for concurrent use; the
// worker loop only ever calls it from its own goroutine.
type backoff struct {
	min, max time.Duration
	current  time.Duration
	rng      *rand.Rand
}

func newBackoff(min, max time.Duration, rng *rand.Rand) *backoff {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &backoff{min: min, max: max, current: min, rng: rng}
}

func (b *backoff) Next() time.Duration {
	if b.current <= 0 {
		return 0
	}
	d := time.Duration(b.rng.Int63n(int64(b.current) + 1))
	next := b.current * 2
	if next > b.max || next <= 0 {
		next = b.max
	}
	b.current = next
	return d
}

func (b *backoff) Reset() {
	b.current = b.min
}
