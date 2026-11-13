package ducklake

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPayloadTransactionErrorStageIsSafe(t *testing.T) {
	lake := embeddedPayloadLake(t)
	sink := lakePayloadSink{lake}
	// The constraint contains an application value. It must not reach the error.
	err := sink.putBatch(context.Background(), []payloadRecord{{"reject", []byte("payload-secret-canary")}})
	if err == nil || !strings.Contains(err.Error(), "DuckLake payload appender flush:") {
		t.Fatalf("missing safe transaction stage: %v", err)
	}
	for _, secret := range []string{"reject", "payload-secret-canary", "CHECK", "INSERT"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("native data reached error: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = sink.putBatch(ctx, []payloadRecord{{"not-written", nil}})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "session wait") {
		t.Fatalf("lost cancellation or stage: %v", err)
	}
	if err = sink.putBatch(context.Background(), []payloadRecord{{"healthy", []byte("ok")}}); err != nil {
		t.Fatalf("failed transaction damaged the retained session: %v", err)
	}
}
