package ducklake

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
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

// getPayloadRows uses bound parameters and copies BLOBs before advancing rows.
// Chunking bounds the statement and driver parameter storage for large backfills.
func getPayloadRows(ctx context.Context, conn *sql.Conn, keys []string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for start := 0; start < len(keys); start += 512 {
		end := min(start+512, len(keys))
		args := make([]any, end-start)
		for i, key := range keys[start:end] {
			args[i] = key
		}
		query := "SELECT payload_key, payload FROM lake.main.looster_payloads WHERE payload_key IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")"
		rows, err := conn.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			var value []byte
			if err = rows.Scan(&key, &value); err != nil {
				break
			}
			if old, ok := result[key]; ok && !bytes.Equal(old, value) {
				err = ErrPayloadConflict
				break
			}
			result[key] = append([]byte{}, value...)
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s lakePayloadSink) getMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	var result map[string][]byte
	var conflict bool
	err := s.lake.withConn(ctx, "payload query", func(ctx context.Context, conn *sql.Conn) error {
		var err error
		result, err = getPayloadRows(ctx, conn, keys)
		conflict = errors.Is(err, ErrPayloadConflict)
		return err
	})
	if conflict {
		return nil, ErrPayloadConflict
	}
	return result, err
}

// putBatch appends bounded input directly into the attached lake catalog. No
// temporary table or SQL literal contains payload data. The session gate covers
// validation, appender close (which flushes its last chunk), and commit together.
func (s lakePayloadSink) putBatch(ctx context.Context, records []payloadRecord) error {
	if len(records) == 0 {
		return ctx.Err()
	}
	conflict := false
	stage := "session wait"
	err := s.lake.withConn(ctx, "payload transaction", func(ctx context.Context, conn *sql.Conn) error {
		stage = "begin"
		if _, err := conn.ExecContext(ctx, "BEGIN TRANSACTION"); err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				// A canceled request must still roll back the retained session before reuse.
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = conn.ExecContext(cleanup, "ROLLBACK")
			}
		}()
		keys := make([]string, 0, len(records))
		unique := make(map[string][]byte, len(records))
		for _, r := range records {
			if old, ok := unique[r.Key]; ok {
				if !bytes.Equal(old, r.Value) {
					conflict = true
					return ErrPayloadConflict
				}
				continue
			}
			unique[r.Key] = r.Value
			keys = append(keys, r.Key)
		}
		stage = "existing payload query"
		existing, err := getPayloadRows(ctx, conn, keys)
		if err != nil {
			conflict = errors.Is(err, ErrPayloadConflict)
			return err
		}
		for key, value := range existing {
			if !bytes.Equal(value, unique[key]) {
				conflict = true
				return ErrPayloadConflict
			}
		}
		stage = "appender creation"
		err = conn.Raw(func(raw any) error {
			appender, err := duckdb.NewAppender(raw.(driver.Conn), "lake", "main", "looster_payloads")
			if err != nil {
				return err
			}
			stage = "append"
			for _, key := range keys {
				if _, ok := existing[key]; ok {
					continue
				}
				if err = ctx.Err(); err != nil {
					break
				}
				if err = appender.AppendRow(key, unique[key]); err != nil {
					break
				}
			}
			if err == nil {
				stage = "appender flush"
			}
			closeErr := appender.CloseWithCancel(ctx)
			if err != nil {
				return err
			}
			return closeErr
		})
		if err != nil {
			return err
		}
		stage = "catalog commit"
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		committed = true
		return nil
	})
	if conflict {
		return ErrPayloadConflict
	}
	if err != nil {
		// Only fixed stage names and the sanitized engine error can escape.
		// Keep cancellation identity for durable retry decisions.
		return fmt.Errorf("DuckLake payload %s: %w", stage, err)
	}
	return nil
}
