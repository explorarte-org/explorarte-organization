package coderunner

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"sync"
	"sync/atomic"
)

const (
	defaultHeadBudget = 128 << 10 // 128 KiB
	defaultTailBudget = 64 << 10  // 64 KiB
	truncationMarker  = "\n...[coderunner: output truncated, digest covers the complete stream]...\n"
)

// ringBuffer retains only the most recently written tailMax bytes without
// ever copying more than the newly written slice on each Write.
type ringBuffer struct {
	buf  []byte
	pos  int
	full bool
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, size)}
}

func (r *ringBuffer) Write(p []byte) {
	size := len(r.buf)
	if size == 0 || len(p) == 0 {
		return
	}
	if len(p) >= size {
		copy(r.buf, p[len(p)-size:])
		r.pos = 0
		r.full = true
		return
	}
	n := copy(r.buf[r.pos:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
		r.pos = len(p) - n
		r.full = true
	} else {
		r.pos += n
		if r.pos == size {
			r.pos = 0
			r.full = true
		}
	}
}

func (r *ringBuffer) Bytes() []byte {
	if !r.full {
		out := make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}
	size := len(r.buf)
	out := make([]byte, size)
	copy(out, r.buf[r.pos:])
	copy(out[size-r.pos:], r.buf[:r.pos])
	return out
}

// outputBudget is a trusted, plan-level aggregate guard on real bytes
// produced across every operation in a single plan execution. It is never
// derived from task-supplied instructions.
type outputBudget struct {
	max     int64
	written int64
}

func newOutputBudget(max int64) *outputBudget {
	return &outputBudget{max: max}
}

func (b *outputBudget) add(n int64) {
	if b == nil {
		return
	}
	atomic.AddInt64(&b.written, n)
}

func (b *outputBudget) exceeded() bool {
	if b == nil || b.max <= 0 {
		return false
	}
	return atomic.LoadInt64(&b.written) > b.max
}

// boundedResult is the durable-evidence-facing snapshot of a captured stream:
// bounded HEAD+TAIL retention alongside the SHA-256 digest of the complete
// stream (including whatever was discarded in between).
type boundedResult struct {
	Head         []byte
	Tail         []byte
	TotalBytes   int64
	Truncated    bool
	DigestSHA256 string
}

func (r boundedResult) String() string {
	if !r.Truncated {
		return string(r.Head)
	}
	return string(r.Head) + truncationMarker + string(r.Tail)
}

// boundedOutput is a thread-safe io.Writer that retains the first headMax
// bytes and the last tailMax bytes of everything written to it, while
// hashing the complete stream (including discarded middle bytes) and
// tracking the true total byte count. Write never blocks on downstream I/O
// and always reports the caller's full len(p), so a slow or adversarial
// consumer can never make a writer believe a partial write occurred.
type boundedOutput struct {
	mu      sync.Mutex
	headMax int
	tailMax int
	head    []byte
	tail    *ringBuffer
	total   int64
	hasher  hash.Hash
	budget  *outputBudget
}

func newBoundedOutput(headMax, tailMax int, budget *outputBudget) *boundedOutput {
	if headMax <= 0 {
		headMax = defaultHeadBudget
	}
	if tailMax <= 0 {
		tailMax = defaultTailBudget
	}
	return &boundedOutput{
		headMax: headMax,
		tailMax: tailMax,
		head:    make([]byte, 0, headMax),
		tail:    newRingBuffer(tailMax),
		hasher:  sha256.New(),
		budget:  budget,
	}
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	w.mu.Lock()
	w.hasher.Write(p)
	w.total += int64(n)
	if len(w.head) < w.headMax {
		room := w.headMax - len(w.head)
		if room > len(p) {
			room = len(p)
		}
		w.head = append(w.head, p[:room]...)
	}
	w.tail.Write(p)
	w.mu.Unlock()
	if w.budget != nil {
		w.budget.add(int64(n))
	}
	return n, nil
}

func (w *boundedOutput) Result() boundedResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	head := make([]byte, len(w.head))
	copy(head, w.head)
	truncated := w.total > int64(w.headMax)+int64(w.tailMax)
	var tail []byte
	if truncated {
		tail = w.tail.Bytes()
	}
	digest := hex.EncodeToString(w.hasher.Sum(nil))
	return boundedResult{Head: head, Tail: tail, TotalBytes: w.total, Truncated: truncated, DigestSHA256: digest}
}
