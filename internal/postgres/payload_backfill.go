package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const backfillPayloadPrefix = "looster-payload:v1:"

// PayloadBackfillReport counts individual nonempty payload fields, not rows.
// CleanupReady is deliberately always false: an operator must fence old writers
// and run an exhaustive VerifyPayloads before considering any separate cleanup.
type PayloadBackfillReport struct {
	Scanned             int  `json:"scanned"`
	Migrated            int  `json:"migrated"`
	References          int  `json:"references"`
	Legacy              int  `json:"legacy"`
	Complete            bool `json:"complete"`
	CleanupReady        bool `json:"cleanup_ready"`
	WriterFenceRequired bool `json:"writer_fence_required"`
}

type backfillColumn struct {
	table, id, column, predicate string
	binary                       bool
}

var backfillColumns = []backfillColumn{
	{"events", "id", "body", "kind IN ('agent_output','system','worker_started','worker_stopped','worker_error','worker_start_failed','scheduler_failed','scheduler_dispatched')", false},
	{"session_logs", "id", "body", "true", false},
	{"jobs", "id", "history", "true", true},
	{"jobs", "id", "summary", "true", false},
	{"jobs", "id", "error", "true", false},
	{"agent_session_runs", "job_id", "summary", "true", false},
	{"agent_session_runs", "job_id", "error", "true", false},
}

func (c backfillColumn) valueSQL() string {
	if c.binary {
		return c.column
	}
	return "convert_to(" + c.column + ",'UTF8')"
}
func backfillKey(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func backfillReference(value []byte) (string, bool) {
	text := string(value)
	if !strings.HasPrefix(text, backfillPayloadPrefix) {
		return "", false
	}
	key := strings.TrimPrefix(text, backfillPayloadPrefix)
	decoded, err := hex.DecodeString(key)
	return key, err == nil && len(decoded) == sha256.Size && key == strings.ToLower(key)
}

// BackfillPayloads replaces at most limit legacy fields with content-addressed
// references. Each transaction locks its source rows until remote Put AND Get
// verify the bytes and the source update commits. Interrupted calls can simply
// be repeated. No tables, columns, metadata or remote objects are deleted.
// Fence every writer not configured for payload storage and upgrade all readers
// before this operation. Old binaries cannot hydrate refs. Before any separate
// cleanup, keep that fence in place and require exhaustive verification with
// Complete=true, Legacy=0, and no remote reread/hash errors.
func (s *Store) BackfillPayloads(ctx context.Context, limit int) (report PayloadBackfillReport, err error) {
	report.WriterFenceRequired = true
	if s.payloads == nil {
		return report, errors.New("payload store is not configured")
	}
	if limit <= 0 {
		return report, errors.New("backfill limit must be positive")
	}
	for _, col := range backfillColumns {
		if report.Migrated >= limit {
			return report, nil
		}
		tx, e := s.Pool.Begin(ctx)
		if e != nil {
			return report, e
		}
		committed := false
		e = func() error {
			defer func() {
				if !committed {
					_ = tx.Rollback(ctx)
				}
			}()
			if _, e := tx.Exec(ctx, "SELECT set_config('looster.payload_backfill','on',true)"); e != nil {
				return e
			}
			value := col.valueSQL()
			query := fmt.Sprintf("SELECT %s,%s FROM %s WHERE %s AND octet_length(%s)>0 AND substring(%s from 1 for %d)<>convert_to($1,'UTF8') ORDER BY %s LIMIT $2 FOR UPDATE SKIP LOCKED", col.id, value, col.table, col.predicate, value, value, len(backfillPayloadPrefix), col.id)
			rows, e := tx.Query(ctx, query, backfillPayloadPrefix, limit-report.Migrated)
			if e != nil {
				return e
			}
			type entry struct {
				id    int64
				value []byte
			}
			var entries []entry
			for rows.Next() {
				var v entry
				if e := rows.Scan(&v.id, &v.value); e != nil {
					rows.Close()
					return e
				}
				entries = append(entries, v)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, v := range entries {
				report.Scanned++
				key := backfillKey(v.value)
				if e := s.payloads.Put(ctx, key, v.value); e != nil {
					return fmt.Errorf("upload %s.%s/%d: %w", col.table, col.column, v.id, e)
				}
				remote, e := s.payloads.Get(ctx, key)
				if e != nil {
					return fmt.Errorf("reread payload %s: %w", key, e)
				}
				if !bytes.Equal(remote, v.value) || backfillKey(remote) != key {
					return fmt.Errorf("payload %s failed reread verification", key)
				}
				var ref any = backfillPayloadPrefix + key
				if col.binary {
					ref = []byte(backfillPayloadPrefix + key)
				}
				if _, e := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s=$1 WHERE %s=$2", col.table, col.column, col.id), ref, v.id); e != nil {
					return e
				}
			}
			if e := tx.Commit(ctx); e != nil {
				return e
			}
			committed = true
			report.Migrated += len(entries)
			return nil
		}()
		if e != nil {
			return report, e
		}
	}
	// SKIP LOCKED may hide concurrent work, so only verification can establish completion.
	return report, nil
}

// VerifyPayloads is read-only and verifies every reference against remote bytes.
// A zero limit means an exhaustive scan. A positive limit bounds fields checked,
// with Complete=false if more exist. Run with writers fenced for cleanup proof.
func (s *Store) VerifyPayloads(ctx context.Context, limit int) (report PayloadBackfillReport, err error) {
	report.WriterFenceRequired = true
	if s.payloads == nil {
		return report, errors.New("payload store is not configured")
	}
	if limit < 0 {
		return report, errors.New("verification limit must not be negative")
	}
	for _, col := range backfillColumns {
		value := col.valueSQL()
		query := fmt.Sprintf("SELECT %s FROM %s WHERE %s AND octet_length(%s)>0 ORDER BY %s", value, col.table, col.predicate, value, col.id)
		args := []any{}
		if limit > 0 {
			query += " LIMIT $1"
			args = append(args, limit-report.Scanned+1)
		}
		rows, e := s.Pool.Query(ctx, query, args...)
		if e != nil {
			return report, e
		}
		for rows.Next() {
			if limit > 0 && report.Scanned >= limit {
				rows.Close()
				return report, nil
			}
			var data []byte
			if e := rows.Scan(&data); e != nil {
				rows.Close()
				return report, e
			}
			report.Scanned++
			if !strings.HasPrefix(string(data), backfillPayloadPrefix) {
				report.Legacy++
				continue
			}
			key, valid := backfillReference(data)
			if !valid {
				rows.Close()
				return report, fmt.Errorf("invalid payload reference in %s.%s", col.table, col.column)
			}
			remote, e := s.payloads.Get(ctx, key)
			if e != nil {
				rows.Close()
				return report, fmt.Errorf("verify payload %s: %w", key, e)
			}
			if backfillKey(remote) != key {
				rows.Close()
				return report, fmt.Errorf("payload %s hash mismatch", key)
			}
			report.References++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return report, e
		}
	}
	report.Complete = true
	return report, nil
}
