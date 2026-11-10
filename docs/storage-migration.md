# Lake payload cutover and historical migration

This upgrade moves heavyweight output, session log bodies, archive retry bytes,
run summaries and failure diagnostics to the mandatory DuckLake payload store.
PostgreSQL retains ordered event identities, workflow comments and triggers,
leases, session bindings, task/agent/job attribution, timestamps and small
content-addressed payload references. Keeping these control records is required
for dispatch, pagination, authorization, deletion history and attachments.

## Deployment sequence

1. Stop old binaries and upgrade **all readers and writers together**. Old
   binaries cannot resolve lake references. A mixed-version rolling deployment
   is not supported. Back up PostgreSQL and preserve existing archive objects.
2. Supply the normal PostgreSQL and S3 endpoint/bucket/region/credential settings.
   Runtime automatically migrates the namespace, opens the built-in DuckDB
   engine, downloads signed extensions if needed, initializes its namespace-specific
   catalog schema and S3 prefix, and activates a durable PostgreSQL writer fence.
   The fence drains in-flight payload writers and rejects subsequent legacy
   inline writes. Startup requires schema migration and catalog permissions.
3. Test normal command execution and archive resume before historical migration.
   Existing inline historical values remain readable until replaced. New writes
   publish PostgreSQL references only after remote lake commit succeeds.
4. Repeat bounded historical batches, then reconcile:

   ```sh
   looster storage backfill --confirm-readers-upgraded --limit 1000
   looster storage verify
   ```

   Select the same namespace for every command, for example with
   `--namespace team-a`. Each backfill invocation reports `migrated` fields.
   Repeat until it reports zero. `verify` defaults to an exhaustive scan. A
   positive `--limit` is diagnostic only and cannot establish completeness.
   Verification must report `complete: true`, `legacy: 0`, and no read/hash error.
   It reads committed remote content again rather than trusting upload success.
   `writer_fence_required` reminds operators to keep the deployment fence in
   place, including for direct SQL clients. No disable-fence API is provided.
5. Keep monitoring session output, logs, inspector history and archive retries.
   Backfill is restartable and content-addressed. Failed remote writes leave the
   source bytes in PostgreSQL, while ambiguous committed uploads can be retried.
   Do not restore an old binary against a cut-over namespace.

## Cleanup is intentionally not authorized

`cleanup_ready` remains **false**. Neither command drops columns/tables, deletes
remote objects, expires snapshots, or vacuums live data. This implementation
reclaims heavyweight column *values* by replacing them with small references.
The current reference-bearing columns and control tables are still used by
readers and **must not be dropped**. A separate schema-removal change must first
move the remaining references and metadata and prove all readers/writers no
longer depend on the old objects. This is a safety gate, not evidence that
arbitrary PostgreSQL tables can now be deleted.

Historical backfill does not infer job identities for legacy output. Existing
S3 session archive objects remain at their original keys. Unreferenced lake
payloads from a failed PostgreSQL transaction may remain, deliberately favoring
safe retention over speculative deletion. Large-scale compaction, retention and
orphan garbage collection are separate maintenance work.

## Verification

The automated acceptance test uses only explicit disposable fixture settings:

```sh
LOOSTER_STORAGE_ACCEPTANCE=yes \
LOOSTER_TEST_DATABASE_URL='postgres://postgres@localhost/looster_test?sslmode=disable' \
LOOSTER_TEST_S3_ENDPOINT='http://localhost:9000' \
LOOSTER_TEST_S3_BUCKET='looster-test' \
LOOSTER_TEST_S3_ACCESS_KEY='test-access' \
LOOSTER_TEST_S3_SECRET_KEY='test-secret' \
go test ./internal/cli -run TestStorageAcceptance -count=1 -v
```

Never substitute live task storage. CI starts checksum-verified disposable MinIO
alongside PostgreSQL and runs the real DuckDB/DuckLake/S3 acceptance path. The
acceptance test checks a binary archive larger than the normal batch target,
a second owner reading before the first shuts down, archive lease reclaim,
public reader hydration, historical backfill and remote hash reconciliation.
Unit and disposable PostgreSQL tests separately cover retry/replay, bounded
batches, immutable history guards, namespace isolation and legacy-writer fencing.
These tests do not establish production-scale latency, retention or cleanup
safety for removing the reference-bearing schema.
