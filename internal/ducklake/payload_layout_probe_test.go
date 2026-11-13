package ducklake

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This probe uses a disposable catalog and the pinned engine and extension.
// It does not change the payload table layout used by the application.
func TestPayloadLayoutPlanProbe(t *testing.T) {
	lake, _ := cachedPayloadInitLake(t)
	ctx := context.Background()
	err := lake.withConn(ctx, "layout probe", func(ctx context.Context, conn *sql.Conn) error {
		var version string
		if err := conn.QueryRowContext(ctx, `SELECT version()`).Scan(&version); err != nil {
			return err
		}
		if version != EngineVersion {
			return fmt.Errorf("unexpected engine version: %s", version)
		}
		var extensionBuild string
		if err := conn.QueryRowContext(ctx, `SELECT extension_version FROM duckdb_extensions() WHERE extension_name='ducklake' AND loaded`).Scan(&extensionBuild); err != nil {
			return err
		}
		if extensionBuild != DuckLakeExtensionVersion {
			return fmt.Errorf("unexpected extension build: %s", extensionBuild)
		}
		t.Logf("Engine %s. DuckLake release %s. Extension build %s.", version, DuckLakeVersion, extensionBuild)
		keys := []string{}
		for _, i := range []int{1, 4096, 8191} {
			hash := sha256.Sum256([]byte(fmt.Sprint(i)))
			keys = append(keys, hex.EncodeToString(hash[:]))
		}
		for _, layout := range []string{"plain", "bucket", "sorted"} {
			name := "probe_" + layout
			if _, err := conn.ExecContext(ctx, `CREATE TABLE lake.main.`+name+`(payload_key VARCHAR,payload BLOB)`); err != nil {
				return err
			}
			if layout == "bucket" {
				if _, err := conn.ExecContext(ctx, `ALTER TABLE lake.main.`+name+` SET PARTITIONED BY (bucket(256,payload_key))`); err != nil {
					return err
				}
			}
			if layout == "sorted" {
				if _, err := conn.ExecContext(ctx, `ALTER TABLE lake.main.`+name+` SET SORTED BY (payload_key)`); err != nil {
					t.Logf("Sorted layout is not supported. Error category: %s.", nativeErrorCategory(err))
					continue
				}
			}
			started := time.Now()
			if _, err := conn.ExecContext(ctx, `INSERT INTO lake.main.`+name+` SELECT sha256(i::VARCHAR),repeat('x',1024)::BLOB FROM range(8192) r(i)`); err != nil {
				return err
			}
			writeTime := time.Since(started)
			var files, fileBytes, minBytes, maxFileBytes int64
			if err := conn.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(data_file_size_bytes),0)::BIGINT,coalesce(min(data_file_size_bytes),0)::BIGINT,coalesce(max(data_file_size_bytes),0)::BIGINT FROM ducklake_list_files('lake',?)`, name).Scan(&files, &fileBytes, &minBytes, &maxFileBytes); err != nil {
				return err
			}
			t.Logf("Layout %s. Files %d. Stored file bytes %d. Minimum file bytes %d. Maximum file bytes %d.", layout, files, fileBytes, minBytes, maxFileBytes)
			_, indexErr := conn.ExecContext(ctx, `CREATE INDEX `+name+`_key ON lake.main.`+name+`(payload_key)`)
			t.Logf("Layout %s. Rows 8192. Payload bytes 8388608. Write time %s. Index supported: %t. Error category: %s.", layout, writeTime, indexErr == nil, nativeErrorCategory(indexErr))
			if indexErr != nil {
				t.Logf("Disposable index probe result: %s", indexErr)
			}
			query := `SELECT payload_key,payload FROM lake.main.` + name + ` WHERE payload_key IN (?,?,?)`
			args := []any{keys[0], keys[1], keys[2]}
			for attempt := 0; attempt < 2; attempt++ {
				profilePath := filepath.Join(t.TempDir(), "profile.json")
				if _, err := conn.ExecContext(ctx, "SET enable_profiling='json'; SET profiling_output="+quote(profilePath)); err != nil {
					t.Logf("Disposable profiling setup result: %s", err)
					return err
				}
				started = time.Now()
				rows, err := conn.QueryContext(ctx, query, args...)
				if err != nil {
					return err
				}
				count := 0
				for rows.Next() {
					var key string
					var payload []byte
					if err = rows.Scan(&key, &payload); err != nil {
						break
					}
					if len(payload) != 1024 {
						rows.Close()
						return fmt.Errorf("unexpected probe payload size: %d", len(payload))
					}
					count++
				}
				if err == nil {
					err = rows.Err()
				}
				rows.Close()
				if err != nil {
					return err
				}
				if count != 3 {
					return fmt.Errorf("unexpected probe row count: %d", count)
				}
				t.Logf("Layout %s. Read %d. Rows %d. Time %s.", layout, attempt+1, count, time.Since(started))
				if _, err := conn.ExecContext(ctx, "PRAGMA disable_profiling"); err != nil {
					return err
				}
				profileBytes, err := os.ReadFile(profilePath)
				if err != nil {
					return err
				}
				var profile struct {
					BytesRead   uint64 `json:"total_bytes_read"`
					RowsScanned uint64 `json:"cumulative_rows_scanned"`
				}
				if err := json.Unmarshal(profileBytes, &profile); err != nil {
					return err
				}
				t.Logf("Layout %s. Read %d. Native bytes read %d. Native rows scanned %d.", layout, attempt+1, profile.BytesRead, profile.RowsScanned)
			}
			rows, err := conn.QueryContext(ctx, `EXPLAIN ANALYZE `+query, args...)
			if err != nil {
				return err
			}
			plans := []string{}
			for rows.Next() {
				var kind, plan string
				if err = rows.Scan(&kind, &plan); err != nil {
					break
				}
				plans = append(plans, plan)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return err
			}
			plan := strings.Join(plans, "\n")
			if !strings.Contains(strings.ToLower(plan), "scan") {
				return fmt.Errorf("probe plan has no scan")
			}
			t.Logf("Layout %s. Point-read plan:\n%s", layout, plan)
		}
		_, keyErr := conn.ExecContext(ctx, `CREATE TABLE lake.main.probe_primary_key(payload_key VARCHAR PRIMARY KEY,payload BLOB)`)
		t.Logf("Primary key supported: %t. Error category: %s.", keyErr == nil, nativeErrorCategory(keyErr))
		if keyErr != nil {
			t.Logf("Disposable primary key probe result: %s", keyErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
