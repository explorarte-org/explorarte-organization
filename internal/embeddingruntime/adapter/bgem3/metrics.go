package bgem3

import (
	"sync/atomic"
	"time"
)

// Metrics are the Go-side counters R30's hardening requirement asks for
// ("métricas de CPU/tiempo real/RSS pico/conteo de textos"). CPU time and
// peak RSS are the sidecar's own numbers (see wire.go's Health, sourced
// from the readiness endpoint) — this type only tracks what the client
// itself can observe: call counts, wall time spent waiting on the sidecar,
// texts submitted, and rejections by cause.
type Metrics struct {
	calls           int64
	failures        int64
	queueRejections int64
	totalTexts      int64
	totalWallTime   int64 // nanoseconds
}

type MetricsSnapshot struct {
	Calls           int64
	Failures        int64
	QueueRejections int64
	TotalTexts      int64
	TotalWallTime   time.Duration
}

func (m *Metrics) recordCall(textCount int, wall time.Duration, failed bool) {
	atomic.AddInt64(&m.calls, 1)
	atomic.AddInt64(&m.totalTexts, int64(textCount))
	atomic.AddInt64(&m.totalWallTime, int64(wall))
	if failed {
		atomic.AddInt64(&m.failures, 1)
	}
}

func (m *Metrics) recordQueueRejection() {
	atomic.AddInt64(&m.queueRejections, 1)
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Calls:           atomic.LoadInt64(&m.calls),
		Failures:        atomic.LoadInt64(&m.failures),
		QueueRejections: atomic.LoadInt64(&m.queueRejections),
		TotalTexts:      atomic.LoadInt64(&m.totalTexts),
		TotalWallTime:   time.Duration(atomic.LoadInt64(&m.totalWallTime)),
	}
}
