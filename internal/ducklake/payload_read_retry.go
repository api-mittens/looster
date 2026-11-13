package ducklake

import (
	"context"
	"errors"
	"time"
)

// retryCommittedPayloadRead is deliberately read-specific. Retrying arbitrary
// callbacks could replay mutations or acknowledge an ambiguous commit.
// The budget includes query execution and backoff, not just time spent asleep.
func retryCommittedPayloadRead(ctx context.Context, budget, initialWait time.Duration, read func(context.Context) (map[string][]byte, error)) (map[string][]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	wait := initialWait
	var lastBusy error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := bounded.Err(); err != nil {
			if lastBusy != nil {
				return nil, lastBusy
			}
			return nil, err
		}
		rows, err := read(bounded)
		if callerErr := ctx.Err(); callerErr != nil {
			return nil, callerErr
		}
		if err == nil {
			return rows, nil
		}
		if !errors.Is(err, ErrCatalogBusy) {
			// A running retry may hit our deadline. Preserve the safe busy identity
			// rather than suggesting that the caller's own deadline was exceeded.
			if bounded.Err() != nil && errors.Is(err, context.DeadlineExceeded) && lastBusy != nil {
				return nil, lastBusy
			}
			return nil, err
		}
		lastBusy = err
		timer := time.NewTimer(wait)
		select {
		case <-bounded.Done():
			timer.Stop()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, lastBusy
		case <-timer.C:
		}
		wait = min(wait*2, time.Second)
	}
}
