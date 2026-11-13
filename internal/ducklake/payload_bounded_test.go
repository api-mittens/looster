package ducklake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestPayloadBoundedCachedDuckLake(t *testing.T) {
	lake, _ := cachedPayloadInitLake(t)
	ctx := context.Background()
	if err := initializePayloadTable(ctx, lake); err != nil {
		t.Fatal(err)
	}
	sink := lakePayloadSink{lake}
	value := bytes.Repeat([]byte{0, 255, 39, 10}, 1024)
	if err := sink.putBatch(ctx, []payloadRecord{{Key: "one", Value: value}, {Key: "two", Value: value}}); err != nil {
		t.Fatal(err)
	}
	store := testPayloadStore(t, sink, PayloadOptions{})
	if _, err := store.GetManyBounded(ctx, []string{"one", "two"}, 8191); !errors.Is(err, ErrPayloadReadLimit) {
		t.Fatalf("parquet byte limit: %v", err)
	}
	rows, err := store.GetManyBounded(ctx, []string{"one", "two"}, 8192)
	if err != nil || !bytes.Equal(rows["one"], value) || !bytes.Equal(rows["two"], value) {
		t.Fatalf("parquet bytes: %v", err)
	}
}

func TestPayloadBoundedSQLRead(t *testing.T) {
	ctx := context.Background()
	lake := embeddedPayloadLake(t)
	sink := lakePayloadSink{lake}
	store := testPayloadStore(t, sink, PayloadOptions{})
	records := []payloadRecord{}
	keys := []string{}
	for i := 0; i < 600; i++ {
		key := fmt.Sprint(i)
		keys = append(keys, key)
		records = append(records, payloadRecord{Key: key, Value: []byte("0123456789")})
	}
	if err := sink.putBatch(ctx, records); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{1, 5119, 5999} {
		if rows, err := store.GetManyBounded(ctx, keys, limit); !errors.Is(err, ErrPayloadReadLimit) || rows != nil {
			t.Fatalf("limit %d: rows %d error %v", limit, len(rows), err)
		}
	}
	rows, err := store.GetManyBounded(ctx, append(keys, keys[0]), 6000)
	if err != nil || len(rows) != 600 {
		t.Fatalf("exact bound: %d %v", len(rows), err)
	}
	if rows, err := store.GetMany(ctx, keys); err != nil || len(rows) != 600 {
		t.Fatalf("full read: %d %v", len(rows), err)
	}
	if _, err := store.GetManyBounded(ctx, keys, 0); !errors.Is(err, ErrPayloadReadLimit) {
		t.Fatal("zero byte limit was accepted")
	}
	if err := sink.putBatch(ctx, []payloadRecord{{Key: "empty", Value: []byte{}}}); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.GetManyBounded(ctx, []string{"empty", "missing"}, 1); err != nil || len(rows) != 1 {
		t.Fatalf("empty or missing value: %v %v", rows, err)
	}
	store.mu.Lock()
	store.pending["overlay"] = []byte("extra")
	store.mu.Unlock()
	if _, err := store.GetManyBounded(ctx, append(keys, "overlay"), 6004); !errors.Is(err, ErrPayloadReadLimit) {
		t.Fatal("overlay bytes were not counted")
	}
	if rows, err := store.GetManyBounded(ctx, append(keys, "overlay"), 6005); err != nil || len(rows) != 601 {
		t.Fatalf("overlay bound: %d %v", len(rows), err)
	}
}

func TestPayloadBoundedReadKeepsConflictCheck(t *testing.T) {
	ctx := context.Background()
	lake := embeddedPayloadLake(t)
	sink := lakePayloadSink{lake}
	if err := lake.Exec(ctx, `INSERT INTO lake.main.looster_payloads VALUES ('duplicate','one'::BLOB),('duplicate','two'::BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.getManyBounded(ctx, []string{"duplicate"}, 100); !errors.Is(err, ErrPayloadConflict) {
		t.Fatalf("conflict check: %v", err)
	}
}
