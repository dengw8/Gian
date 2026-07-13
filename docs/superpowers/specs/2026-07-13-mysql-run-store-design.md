# MySQL Run Store Design

## Goal

Add MySQL as an optional Run Store backend for the Agent Service. The local-file
store remains the default. The selected backend is determined at startup from
`config.yaml`; the Agent runtime and HTTP API continue to depend only on the
existing `store.RunStore` interface.

## Success Criteria

- An unconfigured service behaves exactly as today and uses `storage.driver:
  local` with `storage.local_dir`.
- `storage.driver: mysql` supplies a complete `RunStore` implementation backed
  by MySQL and preserves the current Run Store contract.
- The service uses `code.byted.org/gorm/bytedgorm` for MySQL access.
- MySQL schema changes are supplied as an idempotent SQL script and are applied
  by an operator before the service starts. The service performs no DDL.
- Real connection details, including a password, may live in ignored
  `config.yaml`; `config.example.yaml` contains placeholders only.
- The implementation does not migrate existing local Runs and does not dual
  write. Switching back to `local` makes the existing local data available
  again.

## Non-goals

- No data migration between local files and MySQL.
- No runtime backend switching, replication, read/write splitting, or dual
  writes.
- No deletion, retention, search, or new public API.
- No normalization of the runtime's dynamic message, attachment, tool-call, or
  model-log payloads.

## Configuration

`StorageConfig` gains a `driver` field and a nested MySQL connection config.
The default driver is `local`.

```yaml
storage:
  driver: "local"
  local_dir: "data"
  mysql:
    psm: "agent-service"
    host: "10.37.35.114"
    port: 3306
    database: "agent"
    username: "admin"
    password: "replace-in-ignored-config.yaml"
```

When `driver` is `mysql`, `psm`, `host`, `port`, `database`, `username`, and
`password` are required. `password` is allowed in the ignored local
`config.yaml`; it is never included in `config.example.yaml` as a real value and
must not be written to logs. `AGENT_STORAGE_MYSQL_PASSWORD`, if non-empty,
overrides the YAML password, matching the project convention for credentials.

Invalid drivers and incomplete MySQL configuration fail `Config.Validate`.
The process also fails during startup if it cannot ping the configured database
or cannot find the required tables. Local storage does not require a database
connection.

## Startup Assembly

`cmd/agent-service` selects a concrete store after configuration validation:

- `local`: call the existing `local.New(cfg.Storage.LocalDir)`.
- `mysql`: create `mysql.Store` with the MySQL config, ping the database, verify
  the schema, and pass it to the server as `store.RunStore`.

The MySQL constructor opens GORM with
`gorm.Open(bytedgorm.MySQL(psm, database).With(...))`, explicitly supplying the
DSN constructed from the configured connection values. It uses a direct
connection only; read replicas are deliberately disabled so a Run read directly
after an append cannot observe replica lag. The constructor configures UTF-8,
UTC timestamp parsing, bounded connection-pool defaults, and exposes `Close`
for shutdown. `RunStore` itself remains unchanged.

## Schema

The implementation adds `migrations/mysql/001_create_agent_store.sql`. Operators
run it against the selected database before enabling the MySQL driver. It uses
`CREATE TABLE IF NOT EXISTS`, with indexes declared inside each table definition
for MySQL 5.7 compatibility; it contains no drop, truncate, or data-migration
statement.

| Table | Purpose | Key/indexes |
| --- | --- | --- |
| `agent_runs` | Run metadata, status, errors, token usage, and plan summary | primary key `id`; list index `(updated_at, id)` |
| `agent_run_messages` | Append-only serialized `runtime.Message` values | primary key `id`; index `(run_id, id)` |
| `agent_run_events` | Append-only serialized `runtime.Event` values | primary key `id`; index `(run_id, id)` |
| `agent_model_logs` | Append-only serialized `runtime.ModelLog` values | primary key `id`; index `(run_id, id)` |
| `agent_run_states` | One optional row per Run holding plan and compaction payloads | primary key `run_id` |

All Run IDs are application-generated strings, preserving the current `run_…`
format. The three append-only tables use `BIGINT UNSIGNED AUTO_INCREMENT` IDs
for deterministic retrieval order. Their JSON values are marshalled by the Go
store into `LONGTEXT` columns. This preserves arbitrary runtime payloads and
large base64 image attachments without depending on a particular MySQL JSON
feature set. Metadata timestamps use `DATETIME(6)` and are written and read in
UTC.

`agent_runs` stores token counters as nullable numeric columns and token raw
usage as serialized JSON. `agent_run_states` stores plan and compaction state as
separate nullable `LONGTEXT` values. This keeps the queryable Run summary in
`agent_runs` while retaining the full runtime objects unchanged.

## Run Store Behavior

`internal/store/mysql.Store` implements every existing `RunStore` method.

- `CreateRun` generates an ID, inserts the initial metadata, and returns it.
- `GetRun` and all methods that require an existing Run translate GORM's record
  not-found error to `store.ErrRunNotFound`.
- `ListRuns` uses `updated_at DESC, id DESC`; message, event, and model-log
  listings use their append ID ascending.
- Empty append batches are no-ops. Non-empty batches first validate that the
  Run exists and then batch insert the serialized values.
- `UpdateRun` locks the Run row in a transaction, applies status/error changes,
  adds non-nil token fields to the stored totals, updates `updated_at`, and
  commits atomically.
- `SavePlan` upserts the plan payload and updates the Run's plan summary and
  `updated_at` in the same transaction. `SaveCompaction` upserts only the
  compaction payload after validating the Run.
- Missing plan and compaction rows return `runtime.EmptyPlanState()` and
  `runtime.EmptyCompactionState()`, matching the local store.

Context is supplied to every GORM operation. Context cancellation, database
errors, JSON serialization errors, and schema-validation errors are propagated
without exposing the DSN or password.

## Verification

- Extend configuration tests for the default local driver, valid MySQL
  configuration, environment-password override, and invalid driver/missing
  MySQL fields.
- Run the existing `storetest.RunStoreContract` against the MySQL store in an
  opt-in integration test. It is skipped unless a dedicated test database is
  explicitly configured; it must never target the shared `agent` database or
  delete user data.
- Add focused MySQL tests for schema verification, missing Run mapping,
  append/list ordering, and transactional token and plan updates.
- Run `go test ./...` without a database to preserve the project's current
  deterministic default test experience. When a dedicated test database is
  available, run the opt-in MySQL contract suite as a separate verification
  step.

## Files Expected to Change During Implementation

- `go.mod`, `go.sum`
- `internal/config/config.go`, `internal/config/config_test.go`
- `cmd/agent-service/main.go`
- `internal/store/mysql/` (new implementation and tests)
- `migrations/mysql/001_create_agent_store.sql` (new operator-applied schema)
- `config.example.yaml`
- `README.md`

No files beneath `reference/` are modified.
