# MySQL Run Store Implementation Plan

**Goal:** Add a selectable MySQL-backed `RunStore` using `bytedgorm`, while
keeping local-file storage as the default and requiring operators to apply the
schema SQL before use.

**Precondition:** An operator applies `migrations/mysql/001_create_agent_store.sql`
to the configured database. The application never runs migrations or other DDL.

## 1. Add the MySQL dependencies

**Files:** `go.mod`, `go.sum`

1. Add `code.byted.org/gorm/bytedgorm` at `v0.9.36` and the direct GORM
   dependencies required by the new store.
2. Run `go mod tidy` after the store is added.
3. Confirm `go test ./...` still resolves dependencies without requiring a
   MySQL instance.

## 2. Extend storage configuration

**Files:** `internal/config/config.go`, `internal/config/config_test.go`,
`config.example.yaml`

1. Add `StorageConfig.Driver` and `StorageConfig.MySQL` fields for PSM, host,
   port, database, username, and password.
2. Default `driver` to `local`; retain `local_dir: data` unchanged.
3. Read `AGENT_STORAGE_MYSQL_PASSWORD` after YAML loading and use it when it is
   non-empty; otherwise retain the password in ignored `config.yaml`.
4. Validate MySQL-only fields only when `storage.driver` is `mysql`, and reject
   unknown drivers.
5. Add focused tests for defaults, YAML password, environment override, and
   invalid MySQL configuration. Keep sample credentials as placeholders.

## 3. Supply the operator-applied schema

**Files:** `migrations/mysql/001_create_agent_store.sql`

1. Create `agent_runs`, `agent_run_messages`, `agent_run_events`,
   `agent_model_logs`, and `agent_run_states` using `CREATE TABLE IF NOT
   EXISTS`.
2. Use `VARCHAR` Run IDs, `DATETIME(6)` UTC values, `LONGTEXT` serialized
   payloads, nullable numeric token counters, and `BIGINT UNSIGNED
   AUTO_INCREMENT` append IDs.
3. Define primary keys and the planned indexes inline: `agent_runs` gets
   `(updated_at, id)`; each append table gets `(run_id, id)`.
4. Exclude destructive and migration statements; use MySQL 5.7-compatible DDL.

## 4. Implement the MySQL Store connection and mapping layer

**Files:** `internal/store/mysql/store.go`, `internal/store/mysql/models.go`

1. Define persistence structs matching the five tables and explicit table
   names.
2. Implement `New` with `gorm.Open(bytedgorm.MySQL(...).With(...))`, passing a
   DSN built from configuration with UTF-8 and UTC parsing enabled.
3. Configure a connection pool with at most 10 open and 5 idle connections and
   five-minute idle and lifetime limits; ping the database and verify all five
   required tables exist without running DDL.
4. Add `Close` to release the SQL connection; do not add lifecycle methods to
   `store.RunStore`.
5. Implement marshal/unmarshal helpers for messages, events, logs, token raw
   usage, plan state, and compaction state. Ensure errors never include the DSN
   or password.

## 5. Implement `RunStore` semantics

**Files:** `internal/store/mysql/store.go`, `internal/store/mysql/store_test.go`

1. Implement Run creation, retrieval, and newest-first list ordering.
2. Implement append/list methods for messages, events, and model logs using
   batch inserts and ascending append-ID order.
3. Implement `UpdateRun` in a transaction with a locked Run row so token usage
   increments, status, error, and timestamp update atomically.
4. Implement state upserts: `SavePlan` atomically updates its state payload and
   Run summary; `SaveCompaction` stores only compaction state after Run
   validation.
5. Return empty plan/compaction values for missing state rows and map missing
   Runs to `store.ErrRunNotFound` in every applicable method.
6. Preserve context cancellation and empty-batch no-op behavior.

## 6. Wire store selection into service startup

**Files:** `cmd/agent-service/main.go`, optionally a focused startup helper test

1. Add a small store-construction helper that selects local or MySQL from
   configuration.
2. Pass the resulting `store.RunStore` to `server.New` unchanged.
3. Defer MySQL store closure on normal process shutdown; retain existing local
   startup behavior.
4. Ensure startup failures identify the storage stage without logging secrets.

## 7. Verify and document operation

**Files:** `internal/store/mysql/store_test.go`, `README.md`

1. Reuse `storetest.RunStoreContract` in an opt-in MySQL integration test that
   requires a dedicated test database; skip it by default and never truncate
   the shared `agent` database.
2. Add unit tests for schema verification, ordering, missing Run mapping, and
   transaction-sensitive update paths where practical.
3. Update the README with local-default behavior, the MySQL configuration
   example, password precedence, SQL pre-execution, and the no-migration
   limitation.
4. Run `go test ./...`; report the integration-test status separately if no
   dedicated test database is supplied.
