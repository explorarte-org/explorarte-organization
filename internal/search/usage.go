package search

import (
	"sync"
	"time"
)

type MemoryUsageLedger struct {
	mu      sync.RWMutex
	entries []UsageEntry
}

func NewMemoryUsageLedger() *MemoryUsageLedger {
	return &MemoryUsageLedger{entries: make([]UsageEntry, 0)}
}

func (l *MemoryUsageLedger) Record(m RequestMetrics) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, UsageEntry{
		RequestID:        m.RequestID,
		MissionID:        m.MissionID,
		RoleID:           m.RoleID,
		Provider:         m.Provider,
		Intent:           m.Intent,
		CacheHit:         m.CacheHit,
		EstimatedCostUSD: m.EstimatedCostUSD,
		Duration:         m.Duration,
		ResultCount:      m.ResultCount,
		Success:          m.Success,
		ErrorCode:        m.ErrorCode,
		Timestamp:        time.Now().UTC(),
	})
}

func (l *MemoryUsageLedger) Get(opts UsageLedgerOpts) ([]UsageEntry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var result []UsageEntry
	for _, e := range l.entries {
		if !opts.Since.IsZero() && e.Timestamp.Before(opts.Since) {
			continue
		}
		if !opts.Until.IsZero() && e.Timestamp.After(opts.Until) {
			continue
		}
		if opts.RoleID != "" && e.RoleID != opts.RoleID {
			continue
		}
		if opts.Provider != "" && e.Provider != opts.Provider {
			continue
		}
		if opts.Intent != "" && e.Intent != opts.Intent {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}
