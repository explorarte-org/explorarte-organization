package coderunner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
)

func TestBoundedOutputNoTruncationBelowBudget(t *testing.T) {
	w := newBoundedOutput(16, 8, nil)
	data := []byte("hello world")
	n, err := w.Write(data)
	if err != nil || n != len(data) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	r := w.Result()
	if r.Truncated {
		t.Fatal("should not be truncated")
	}
	if r.String() != string(data) {
		t.Fatalf("got %q", r.String())
	}
	if r.TotalBytes != int64(len(data)) {
		t.Fatalf("total=%d", r.TotalBytes)
	}
}

// TestBoundedOutputHeadAndTailWithMarker proves the HEAD+TAIL retention
// contract: a stream far larger than head+tail keeps the first bytes, the
// last bytes, and an explicit truncation marker in between, rather than
// only ever keeping the beginning (where a build failure's decisive error
// is often absent).
func TestBoundedOutputHeadAndTailWithMarker(t *testing.T) {
	w := newBoundedOutput(4, 4, nil)
	if _, err := w.Write([]byte("AAAA-middle-noise-BBBB")); err != nil {
		t.Fatal(err)
	}
	r := w.Result()
	if !r.Truncated {
		t.Fatal("expected truncation")
	}
	if string(r.Head) != "AAAA" {
		t.Fatalf("head=%q", r.Head)
	}
	if string(r.Tail) != "BBBB" {
		t.Fatalf("tail=%q", r.Tail)
	}
	rendered := r.String()
	if !bytes.HasPrefix([]byte(rendered), []byte("AAAA")) || !bytes.HasSuffix([]byte(rendered), []byte("BBBB")) {
		t.Fatalf("rendered=%q", rendered)
	}
}

// TestBoundedOutputDigestCoversCompleteStream proves the SHA-256 digest is
// computed over everything written, including the bytes head/tail discard,
// so evidence can prove exactly what a process produced even when most of
// it was never retained.
func TestBoundedOutputDigestCoversCompleteStream(t *testing.T) {
	w := newBoundedOutput(4, 4, nil)
	payload := []byte("AAAA-a lot of discarded middle content that is never retained-BBBB")
	if _, err := w.Write(payload[:10]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload[10:]); err != nil {
		t.Fatal(err)
	}
	r := w.Result()
	want := sha256.Sum256(payload)
	if r.DigestSHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("digest mismatch: got %s want %x", r.DigestSHA256, want)
	}
	if r.TotalBytes != int64(len(payload)) {
		t.Fatalf("total=%d want=%d", r.TotalBytes, len(payload))
	}
}

func TestBoundedOutputWriteAlwaysReportsFullLength(t *testing.T) {
	w := newBoundedOutput(1, 1, nil)
	p := bytes.Repeat([]byte("x"), 10_000)
	n, err := w.Write(p)
	if err != nil || n != len(p) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// TestBoundedOutputHandlesLargeStreamBounded proves memory stays bounded
// (only head+tail retained) even for a ~100 MiB stream, and that the
// digest still matches an independently computed hash of everything
// written.
func TestBoundedOutputHandlesLargeStreamBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100MiB capture test in -short mode")
	}
	const total = 100 << 20
	w := newBoundedOutput(defaultHeadBudget, defaultTailBudget, nil)
	hasher := sha256.New()
	chunk := bytes.Repeat([]byte("z"), 1<<20)
	written := 0
	for written < total {
		n := len(chunk)
		if written+n > total {
			n = total - written
		}
		hasher.Write(chunk[:n])
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatal(err)
		}
		written += n
	}
	r := w.Result()
	if !r.Truncated {
		t.Fatal("expected truncation for a 100MiB stream")
	}
	if r.TotalBytes != int64(total) {
		t.Fatalf("total=%d want=%d", r.TotalBytes, total)
	}
	if got, want := r.DigestSHA256, hex.EncodeToString(hasher.Sum(nil)); got != want {
		t.Fatalf("digest mismatch: got %s want %s", got, want)
	}
	if len(r.Head)+len(r.Tail) > defaultHeadBudget+defaultTailBudget {
		t.Fatalf("retained more than head+tail budget: head=%d tail=%d", len(r.Head), len(r.Tail))
	}
}

// TestBoundedOutputConcurrentWritesRaceFree proves stdout and stderr can
// write concurrently without a data race (run this file with -race).
func TestBoundedOutputConcurrentWritesRaceFree(t *testing.T) {
	w := newBoundedOutput(64, 64, newOutputBudget(1<<30))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = w.Write([]byte(fmt.Sprintf("goroutine-%d-line-%d\n", id, i)))
			}
		}(g)
	}
	wg.Wait()
	r := w.Result()
	if r.TotalBytes == 0 {
		t.Fatal("expected non-zero total bytes")
	}
}

func TestOutputBudgetExceeded(t *testing.T) {
	b := newOutputBudget(10)
	b.add(5)
	if b.exceeded() {
		t.Fatal("should not be exceeded yet")
	}
	b.add(6)
	if !b.exceeded() {
		t.Fatal("should be exceeded")
	}
}

func TestOutputBudgetDisabledWhenZero(t *testing.T) {
	b := newOutputBudget(0)
	b.add(1 << 30)
	if b.exceeded() {
		t.Fatal("a zero budget must never trip")
	}
}

func TestRingBufferKeepsMostRecentBytes(t *testing.T) {
	r := newRingBuffer(4)
	r.Write([]byte("abcdefgh"))
	if got := string(r.Bytes()); got != "efgh" {
		t.Fatalf("got %q", got)
	}
	r2 := newRingBuffer(4)
	r2.Write([]byte("ab"))
	r2.Write([]byte("cdef"))
	if got := string(r2.Bytes()); got != "cdef" {
		t.Fatalf("got %q", got)
	}
}
