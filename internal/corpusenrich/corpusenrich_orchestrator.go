package corpusenrich

import (
	"context"
	"fmt"
	"time"
)

// OrchestratorConfig bounds batch size (Semantic Scholar's own 500-ID
// cap, but the owner directed "batches conservadores" -- default well
// under that), backoff behavior on 429 (bounded: a fixed number of
// retries with growing sleep, never infinite), and inter-batch pacing
// (a small delay even on success, to stay a well-behaved anonymous
// client rather than push the undocumented rate limit).
type OrchestratorConfig struct {
	BatchSize       int
	MaxRetriesOn429 int
	BackoffBase     time.Duration
	InterBatchDelay time.Duration
	FlushEveryBatch bool
}

func DefaultOrchestratorConfig() OrchestratorConfig {
	return OrchestratorConfig{
		BatchSize: 150, MaxRetriesOn429: 3, BackoffBase: 5 * time.Second,
		InterBatchDelay: 1 * time.Second, FlushEveryBatch: true,
	}
}

type Orchestrator struct {
	Client *Client
	Store  *Store
	Config OrchestratorConfig
}

// RunResult reports exactly what the owner asked for verification on:
// coverage and failure/backoff behavior, not just a final count.
type RunResult struct {
	Requested        int
	AlreadyCached    int
	Fetched          int
	AbstractsFound   int
	AbstractsMissing int
	RateLimitedStop  bool
	StoppedAtBatch   int
	BatchesCompleted int
}

// Run processes paperIDs in Config.BatchSize chunks, skipping any
// already present in Store (resumability), and stops immediately -- not
// after exhausting retries silently -- if MaxRetriesOn429 is reached on
// any single batch. That stop is reported, never swallowed, per the
// owner's explicit instruction: "Si da 429 o bloqueo anónimo, que se
// detenga ahí."
func (o *Orchestrator) Run(ctx context.Context, paperIDs []string) (RunResult, error) {
	result := RunResult{Requested: len(paperIDs)}
	var pending []string
	for _, id := range paperIDs {
		if o.Store.Has(id) {
			result.AlreadyCached++
			continue
		}
		pending = append(pending, id)
	}

	batchSize := o.Config.BatchSize
	if batchSize <= 0 || batchSize > 500 {
		batchSize = 150
	}

	for start := 0; start < len(pending); start += batchSize {
		end := start + batchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]

		records, err := o.fetchWithBackoff(ctx, batch)
		if err != nil {
			if err == ErrRateLimited {
				result.RateLimitedStop = true
				result.StoppedAtBatch = result.BatchesCompleted
				if flushErr := o.Store.Flush(); flushErr != nil {
					return result, fmt.Errorf("corpusenrich: flush after rate-limit stop: %w", flushErr)
				}
				return result, nil
			}
			return result, fmt.Errorf("corpusenrich: batch starting at %d: %w", start, err)
		}

		for _, record := range records {
			o.Store.Put(record)
			result.Fetched++
			if record.HasAbstract() {
				result.AbstractsFound++
			} else {
				result.AbstractsMissing++
			}
		}
		result.BatchesCompleted++

		if o.Config.FlushEveryBatch {
			if err := o.Store.Flush(); err != nil {
				return result, fmt.Errorf("corpusenrich: checkpoint flush: %w", err)
			}
		}

		if o.Config.InterBatchDelay > 0 && end < len(pending) {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(o.Config.InterBatchDelay):
			}
		}
	}

	if err := o.Store.Flush(); err != nil {
		return result, fmt.Errorf("corpusenrich: final flush: %w", err)
	}
	return result, nil
}

func (o *Orchestrator) fetchWithBackoff(ctx context.Context, batch []string) ([]AbstractRecord, error) {
	var lastErr error
	for attempt := 0; attempt <= o.Config.MaxRetriesOn429; attempt++ {
		records, err := o.Client.FetchBatch(ctx, batch)
		if err == nil {
			return records, nil
		}
		if err != ErrRateLimited {
			return nil, err
		}
		lastErr = err
		if attempt == o.Config.MaxRetriesOn429 {
			break
		}
		sleep := o.Config.BackoffBase * time.Duration(1<<attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}
	}
	return nil, lastErr
}
