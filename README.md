# Agent Service

This repository contains a standalone Go Agent Service. It runs a ReAct-style agent loop with durable Runs, multi-Turn execution, structured events, Workspace Tools, a conservative Shell Tool safety policy, and configurable Model Providers.

The implementation lives in this repository. The projects under `reference/` are comparison material and are not modified by the service.

## Requirements

- Go 1.22 or newer
- `make`

## Configuration

Start from the sample config:

```bash
cp config.example.yaml config.yaml
```

Default config:

```yaml
server:
  addr: ":8080"
model:
  provider: "fake"
  openai:
    base_url: "https://api.openai.com/v1"
    model: "gpt-4.1-mini"
  modelhub:
    endpoint: "https://aidp.bytedance.net/api/modelhub/online/v2/crawl"
    model: "gpt-5.4-2026-03-05"
    max_tokens: 500
agent:
  max_iterations: 4
storage:
  driver: "local"
  local_dir: "data"
tools:
  workspace: "."
```

The service reads `config.yaml` by default. To use another file:

```bash
AGENT_CONFIG=/path/to/config.yaml make run
```

## Run

```bash
make run
```

This starts the service on `server.addr`.

Open the embedded web console:

```text
http://localhost:8080/app/
```

## Test

```bash
make test
```

Tests use the Fake Model and do not require network access.

## Model Providers

### Fake Model

Fake Model mode is the default:

```yaml
model:
  provider: "fake"
```

It is deterministic and useful for local development and tests.

Fake Model trigger examples:

- `read_file:README.md`
- `list_files:.`
- `shell:printf hello`

### OpenAI-Compatible Provider

Use `openai_compatible` for a Chat Completions-compatible endpoint:

```yaml
model:
  provider: "openai_compatible"
  openai:
    base_url: "https://api.openai.com/v1"
    model: "gpt-4.1-mini"
```

Provide the API key through the environment:

```bash
AGENT_MODEL_API_KEY=... make run
```

The API key is intentionally not read from `config.yaml`.

### ModelHub Provider

Use `modelhub` for the ByteDance ModelHub crawl endpoint:

```yaml
model:
  provider: "modelhub"
  modelhub:
    endpoint: "https://aidp.bytedance.net/api/modelhub/online/v2/crawl"
    model: "gpt-5.4-2026-03-05"
    max_tokens: 500
```

Provide the AK through the environment:

```bash
AGENT_MODELHUB_AK=... make run
```

Or put `ak` in a local `config.yaml` that is ignored by git. `X-TT-LOGID` is generated automatically when it is not configured.

## API

All examples assume:

```bash
BASE=http://localhost:8080
```

### List Runs

Fetch durable Run metadata, newest updated first:

```bash
curl -sS "$BASE/v1/runs"
```

Response:

```json
{
  "runs": [
    {
      "id": "run_...",
      "status": "completed",
      "model_provider": "fake"
    }
  ]
}
```

### Create a Run

Create an empty durable Run:

```bash
curl -sS "$BASE/v1/runs" \
  -H 'content-type: application/json' \
  -d '{}'
```

Create a Run and execute the first Turn:

```bash
curl -sS "$BASE/v1/runs" \
  -H 'content-type: application/json' \
  -d '{"message":"hello"}'
```

Create a Run in Plan Mode:

```bash
curl -sS "$BASE/v1/runs" \
  -H 'content-type: application/json' \
  -d '{"message":"please plan this change","plan_mode":true}'
```

Create a Run with an inline image attachment:

```bash
curl -sS "$BASE/v1/runs" \
  -H 'content-type: application/json' \
  -d '{
    "message": "describe this image",
    "attachments": [
      {
        "type": "image",
        "media_type": "image/png",
        "data": "...base64 image data...",
        "name": "image.png"
      }
    ]
  }'
```

Response includes Run metadata and messages:

```json
{
  "run": {
    "id": "run_...",
    "status": "completed",
    "model_provider": "fake"
  },
  "messages": [
    {"role": "user", "content": "hello"},
    {"role": "assistant", "content": "fake response: hello"}
  ]
}
```

### Append a Turn

```bash
RUN_ID=run_...

curl -sS "$BASE/v1/runs/$RUN_ID/turns" \
  -H 'content-type: application/json' \
  -d '{"message":"second message"}'
```

The response includes the full Run messages and `new_messages` for this Turn.

### Stream a Turn

```bash
curl -N "$BASE/v1/runs/$RUN_ID/turns/stream" \
  -H 'content-type: application/json' \
  -d '{"message":"read_file:README.md"}'
```

The endpoint returns Server-Sent Events:

```text
event: turn_started
data: {...}

event: model_started
data: {...}

event: model_output_delta
data: {"data":{"delta":"partial output"}, ...}

event: model_completed
data: {...}

event: tool_started
data: {...}

event: tool_completed
data: {...}

event: turn_completed
data: {...}
```

Streamed events are also persisted. Each persisted event has stable `id` and `run_id` fields; execution events additionally carry `turn_id`, and model and tool events carry their respective `model_call_id` or `tool_call_id`. `model_output_delta` events contain the incremental text in `data.delta` and are emitted as the OpenAI-compatible provider receives each stream chunk.

`model_completed` events include per-call `usage` when the configured Model Provider returns token accounting. Providers that do not return usage leave the field absent.

### Fetch Run Metadata

```bash
curl -sS "$BASE/v1/runs/$RUN_ID"
```

Run metadata includes cumulative `token_usage` when usage is available:

```json
{
  "token_usage": {
    "prompt_tokens": 16,
    "completion_tokens": 5,
    "total_tokens": 21,
    "cached_tokens": 0,
    "reasoning_tokens": 0
  }
}
```

### Fetch Messages

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/messages"
```

### Fetch Events

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/events"
```

### Fetch Model I/O Logs

Fetch the final model request and response for each model call in a Run:

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/model-logs"
```

These logs contain the assembled system prompt, messages, tool definitions, assistant response, provider usage, and model errors. Provider credentials are not included.

### Plan Mode

Enable read-only planning for a Run:

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/plan-mode" \
  -H 'content-type: application/json' \
  -d '{"enabled":true}'
```

Fetch the persisted Plan State:

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/plan"
```

Execute a generated plan:

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/plan/execute" \
  -H 'content-type: application/json' \
  -d '{}'
```

Reset a plan:

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/plan/reset" \
  -H 'content-type: application/json' \
  -d '{}'
```

While planning, the runtime restricts tools to read-only exploration. During execution, the runtime injects the persisted steps and tracks `[DONE:n]` completion markers.

## Tools

### `read_file`

Reads a workspace-relative file:

```bash
curl -sS "$BASE/v1/runs" \
  -H 'content-type: application/json' \
  -d '{"message":"read_file:README.md"}'
```

### `list_files`

Lists direct children of a workspace-relative directory:

```bash
curl -sS "$BASE/v1/runs" \
  -H 'content-type: application/json' \
  -d '{"message":"list_files:."}'
```

### `shell`

Runs an approved command under `tools.workspace`:

```bash
curl -sS "$BASE/v1/runs" \
  -H 'content-type: application/json' \
  -d '{"message":"shell:printf hello"}'
```

The Shell Tool rejects destructive commands and command chaining before execution. For example, `shell:rm file.txt` returns a rejected tool result instead of executing.

## Persistence

`storage.driver` defaults to `local`. Local persistence is stored under
`storage.local_dir`:

```text
data/runs/<run_id>/
  metadata.json
  plan.json
  messages.jsonl
  events.jsonl
  model_logs.jsonl
  trace.jsonl
```

`messages.jsonl`, `events.jsonl`, and `model_logs.jsonl` are append-only JSON Lines files.

Token usage is persisted in two places:

- Per model call on `model_completed` events in `events.jsonl`.
- Cumulative Run totals on `metadata.json`.

`cached_tokens` is populated from provider usage details when the provider reports prompt-cache hits.

Model I/O logs are persisted in `model_logs.jsonl` for reproducible debugging. Each entry stores the final request passed to the Model Provider and the response or error returned by that provider.

### Execution Trace

Trace is an optional, default-enabled best-effort observer for newly created Runs:

```yaml
trace:
  enabled: true
```

It records an asynchronous `Run → Turn → model/tool/approval` tree in
`trace.jsonl` for local storage (or `agent_trace_spans` in MySQL). Trace writes
never block or fail a Run; a slow or failed observer can leave an incomplete
tree. Trace payloads redact common credential fields and omit attachment binary
data. Existing Runs are not backfilled.

The root Run span measures the active agent execution slice rather than time
spent waiting for the next user message. Shell approval waiting time is shown
by the approval span itself.

Open `/app/runs/<run_id>/messages` for the conversation timeline or
`/app/runs/<run_id>/trace` for the manually refreshed execution tree.

Trace uses two complementary views:

- **Tree** shows execution containment: `Run → Turn (graph) → model/tool/approval`.
- **Chat** projects the same spans into user/model/tool-result order. Model tool calls and tool spans are linked by the stable tool-call ID; a tool result is also linked to the next model call that consumes it.

The Trace page supports type/state/text filtering, a bounded “show more” flow
for large Runs, span-level raw JSON copying, type-specific Run details,
Metadata/Runtime, and a lightweight comparison of two Run summaries. Trace
responses include a `diagnostics` object. If its `complete`
field is false, the in-process observer dropped updates or encountered a write
error, so the tree is useful for debugging but is not an audit record.

Trace payloads are intentionally best-effort and are not backfilled. The API
also exposes individual spans and comparisons:

```text
GET  /v1/runs/<run_id>/trace?kind=tool&status=failed&q=shell&limit=120
GET  /v1/runs/<run_id>/trace/spans/<span_id>
GET  /v1/runs/<run_id>/trace/compare/<other_run_id>
```

### MySQL Store

Set `storage.driver` to `mysql` to persist Runs in MySQL:

```yaml
storage:
  driver: "mysql"
  mysql:
    psm: "agent-service"
    host: "mysql.example.com"
    port: 3306
    database: "agent"
    username: "agent_user"
    password: "replace-in-ignored-config.yaml"
```

The password can be stored in the ignored `config.yaml`. A non-empty
`AGENT_STORAGE_MYSQL_PASSWORD` overrides it.

Before starting with the MySQL driver, apply the following migrations to the
selected database:

- [`migrations/mysql/001_create_agent_store.sql`](migrations/mysql/001_create_agent_store.sql)
- [`migrations/mysql/003_add_trace_spans.sql`](migrations/mysql/003_add_trace_spans.sql)

The service only pings MySQL and verifies that the
required tables exist; it never runs migrations or other DDL. Switching
between local and MySQL does not migrate or dual-write existing Run data.

## Current Scope

Implemented:

- Standalone Go service
- Durable Runs
- Run history API
- Multi-Turn execution
- Local Store
- MySQL Store
- Run Store contract tests
- HTTP JSON API
- SSE Turn Event Stream
- Fake Model
- OpenAI-Compatible Provider
- ModelHub Provider
- Run-level Plan Mode
- Provider token/cache usage collection
- Model I/O logging
- Workspace Tools
- Shell Tool with conservative safety policy
- Message timestamps
- Inline image attachments
- Embedded Web Console

Out of scope for this version:

- Docker packaging
- Database-backed Run Store
- MCP integration
- Production context compaction
- Deep subagent recursion
- Fornax trace backend integration
- Full shell AST security analysis
