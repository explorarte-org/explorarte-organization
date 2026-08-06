package cellworker

import (
	"math"
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
	// Int63n panics on n<=0; int64(b.current)+1 overflows only at the
	// upper bound of time.Duration, which doubling can reach if MaxBackoff
	// is configured absurdly close to it.
	ceiling := int64(b.current)
	var d time.Duration
	if ceiling == math.MaxInt64 {
		d = time.Duration(b.rng.Int63n(math.MaxInt64))
	} else {
		d = time.Duration(b.rng.Int63n(ceiling + 1))
	}
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
