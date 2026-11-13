package ducklake

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

// Initialization is read-only for configured tables. In particular, existing
// partition layouts belong to the catalog, not to the client opening it.
func initializePayloadTable(ctx context.Context, lake *Lake) error {
	return lake.withConn(ctx, "payload initialization", func(ctx context.Context, conn *sql.Conn) error {
		for attempt := 0; ; attempt++ {
			err := initializePayloadTableAttempt(ctx, conn)
			if err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt >= 4 || !payloadInitTransient(err) {
				return err
			}
			timer := time.NewTimer(time.Duration(25*(1<<attempt)) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	})
}

var errPayloadInitCreateRace = errors.New("payload table concurrently created")

func payloadInitTransient(err error) bool {
	if errors.Is(err, errPayloadInitCreateRace) {
		return true
	}
	var native *duckdb.Error
	return errors.As(err, &native) && native != nil && native.Type == duckdb.ErrorTypeTransaction
}

func initializePayloadTableAttempt(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM duckdb_tables() WHERE database_name = 'lake' AND schema_name = 'main' AND table_name = 'looster_payloads')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		// Do not use IF NOT EXISTS: a competing creator must cause this transaction
		// to retry, rather than letting us repartition its newly committed table.
		// Creation and partitioning commit together, so no client can observe a
		// partially initialized layout, even if this process is canceled or exits.
		if _, err = tx.ExecContext(ctx, `CREATE TABLE lake.main.looster_payloads (payload_key VARCHAR, payload BLOB); ALTER TABLE lake.main.looster_payloads SET PARTITIONED BY (bucket(256, payload_key))`); err != nil {
			// A competing CREATE can report Catalog instead of Transaction.
			// Retry only when a fresh transaction sees the committed table.
			var native *duckdb.Error
			if errors.As(err, &native) && native != nil && native.Type == duckdb.ErrorTypeCatalog {
				if rollbackErr := tx.Rollback(); rollbackErr != nil {
					return err
				}
				var appeared bool
				if checkErr := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM duckdb_tables() WHERE database_name = 'lake' AND schema_name = 'main' AND table_name = 'looster_payloads')`).Scan(&appeared); checkErr == nil && appeared {
					return errPayloadInitCreateRace
				}
			}
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// set_option resolves committed table IDs. Keep it outside the creation
	// transaction and recheck it on every retry, including after a commit conflict.
	var configured bool
	if err = conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM lake.options() WHERE scope = 'TABLE' AND scope_entry = 'main.looster_payloads' AND option_name = 'data_inlining_row_limit' AND try_cast(value AS BIGINT) = 0)`).Scan(&configured); err != nil {
		return err
	}
	if configured {
		return nil
	}
	_, err = conn.ExecContext(ctx, `CALL lake.set_option('data_inlining_row_limit', 0, table_name => 'looster_payloads', schema => 'main')`)
	return err
}
