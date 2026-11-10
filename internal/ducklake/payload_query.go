package ducklake

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type lakePayloadSink struct{ lake *Lake }

func (s lakePayloadSink) get(ctx context.Context, key string) ([]byte, error) {
	rows, err := s.getMany(ctx, []string{key})
	if err != nil {
		return nil, err
	}
	v, ok := rows[key]
	if !ok {
		return nil, ErrPayloadNotFound
	}
	return v, nil
}

// getMany uses exactly one COPY instead of CLI stdout, which also contains the
// credential prelude's results. Hex columns keep arbitrary keys and binary data
// unambiguous, including empty values. Duplicate identical rows are folded, but
// conflicting content is never selected arbitrarily.
func (s lakePayloadSink) getMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	rows := map[string][]byte{}
	if len(keys) == 0 {
		return rows, nil
	}
	dir, err := os.MkdirTemp("", "looster-payload-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "values.csv")
	var q strings.Builder
	q.WriteString("COPY (SELECT DISTINCT hex(encode(payload_key)), hex(payload) FROM lake.main.looster_payloads WHERE payload_key IN (")
	for i, k := range keys {
		if i > 0 {
			q.WriteByte(',')
		}
		q.WriteString("decode(from_hex(" + quote(hex.EncodeToString([]byte(k))) + "))")
	}
	q.WriteString(")) TO " + quote(path) + " (FORMAT CSV, HEADER false, FORCE_QUOTE *);")
	if err = s.lake.Exec(ctx, q.String()); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.FieldsPerRecord = 2
	for {
		record, e := reader.Read()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return nil, errors.New("invalid DuckLake payload result")
		}
		key, e := hex.DecodeString(record[0])
		if e != nil {
			return nil, errors.New("invalid DuckLake payload key encoding")
		}
		value, e := hex.DecodeString(record[1])
		if e != nil {
			return nil, errors.New("invalid DuckLake payload encoding")
		}
		k := string(key)
		if old, ok := rows[k]; ok && string(old) != string(value) {
			return nil, ErrPayloadConflict
		}
		rows[k] = value
	}
	return rows, nil
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
	// Validate existing content before inserting. Concurrent writers may append
	// duplicate identical SHA256-addressed rows, which getMany folds logically.
	// Put validates SHA256 keys before the transaction, preventing conflicting
	// concurrent content for the references published by PostgreSQL.
	q.WriteString("; SELECT CASE WHEN EXISTS (SELECT 1 FROM lake.main.looster_payloads p JOIN payload_batch b USING(payload_key) WHERE p.payload IS DISTINCT FROM b.payload) THEN error('payload conflict') ELSE true END; INSERT INTO lake.main.looster_payloads SELECT b.* FROM payload_batch b WHERE NOT EXISTS (SELECT 1 FROM lake.main.looster_payloads p WHERE p.payload_key=b.payload_key); COMMIT;")
	return s.lake.Exec(ctx, q.String())
}
