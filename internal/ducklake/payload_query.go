package ducklake

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

type lakePayloadSink struct{ lake *Lake }

// get uses COPY rather than CLI stdout, which also contains the credential
// prelude's query results. The private 0700 directory isolates payload output.
func (s lakePayloadSink) get(ctx context.Context, key string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "looster-payload-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := dir + "/value.csv"
	sql := "COPY (SELECT hex(payload) AS value FROM lake.main.looster_payloads WHERE payload_key=" + quote(key) + ") TO " + quote(path) + " (FORMAT CSV, HEADER false, FORCE_QUOTE *);"
	if err = s.lake.Exec(ctx, sql); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(b))
	if text == "" {
		return nil, ErrPayloadNotFound
	}
	lines := strings.Split(text, "\n")
	var value []byte
	for i, line := range lines {
		v, e := hex.DecodeString(strings.Trim(strings.TrimSpace(line), "\""))
		if e != nil {
			return nil, errors.New("invalid DuckLake payload encoding")
		}
		if i > 0 && string(value) != string(v) {
			return nil, ErrPayloadConflict
		}
		value = v
	}
	return value, nil
}
func (s lakePayloadSink) putBatch(ctx context.Context, records []payloadRecord) error {
	if len(records) == 0 {
		return nil
	}
	var q strings.Builder
	q.WriteString("BEGIN TRANSACTION; CREATE TEMP TABLE payload_batch (payload_key VARCHAR, payload BLOB); INSERT INTO payload_batch VALUES ")
	for i, r := range records {
		if i > 0 {
			q.WriteByte(',')
		}
		q.WriteString("(" + quote(r.Key) + ",from_hex(" + quote(hex.EncodeToString(r.Value)) + "))")
	}
	// DuckLake has no unique constraints. Serialize writers through the durable
	// spool lock. Validate existing content in the same transaction before insert.
	q.WriteString("; SELECT CASE WHEN EXISTS (SELECT 1 FROM lake.main.looster_payloads p JOIN payload_batch b USING(payload_key) WHERE p.payload IS DISTINCT FROM b.payload) THEN error('payload conflict') ELSE true END; INSERT INTO lake.main.looster_payloads SELECT b.* FROM payload_batch b WHERE NOT EXISTS (SELECT 1 FROM lake.main.looster_payloads p WHERE p.payload_key=b.payload_key); COMMIT;")
	return s.lake.Exec(ctx, q.String())
}
