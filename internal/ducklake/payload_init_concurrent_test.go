package ducklake

import (
	"context"
	"sync"
	"testing"
)

func TestPayloadInitCachedDuckLakeConcurrentCreation(t *testing.T) {
	first, attach := cachedPayloadInitLake(t)
	ctx := context.Background()
	second, err := OpenEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = second.Exec(ctx, attach); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, lake := range []*Lake{first, second} {
		wg.Add(1)
		go func(lake *Lake) {
			defer wg.Done()
			<-start
			results <- initializePayloadTable(ctx, lake)
		}(lake)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	before := payloadInitSnapshotCount(t, first)
	for _, lake := range []*Lake{first, second} {
		if err = initializePayloadTable(ctx, lake); err != nil {
			t.Fatal(err)
		}
	}
	if after := payloadInitSnapshotCount(t, first); before != after {
		t.Fatalf("concurrent setup did not converge: %d -> %d", before, after)
	}
}
