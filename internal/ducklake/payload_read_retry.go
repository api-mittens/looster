package ducklake

import (
	"context"
	"errors"
	"time"
)

// retryCommittedPayloadRead is deliberately read-specific. Retrying arbitrary
// callbacks could replay mutations or acknowledge an ambiguous commit.
// The first read keeps the caller's context unchanged. Only a confirmed busy
// error starts a recovery budget including retry execution and backoff.
func retryCommittedPayloadRead(ctx context.Context, budget, initialWait time.Duration, read func(context.Context) (map[string][]byte, error)) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := read(ctx)
	if callerErr := ctx.Err(); callerErr != nil {
		return nil, callerErr
	}
	if err == nil {
		return rows, nil
	}
	if !errors.Is(err, ErrCatalogBusy) {
		return nil, err
	}
	lastBusy := err
	bounded, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	wait := initialWait
	for {
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if bounded.Err() != nil {
			return nil, lastBusy
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
			if bounded.Err() != nil && errors.Is(err, context.DeadlineExceeded) {
				return nil, lastBusy
			}
			return nil, err
		}
		lastBusy = err
		wait = min(wait*2, time.Second)
	}
}
