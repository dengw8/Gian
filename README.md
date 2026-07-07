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
agent:
  max_iterations: 4
storage:
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

## API

All examples assume:

```bash
BASE=http://localhost:8080
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

event: model_completed
data: {...}

event: tool_started
data: {...}

event: tool_completed
data: {...}

event: turn_completed
data: {...}
```

Streamed events are also persisted.

### Fetch Run Metadata

```bash
curl -sS "$BASE/v1/runs/$RUN_ID"
```

### Fetch Messages

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/messages"
```

### Fetch Events

```bash
curl -sS "$BASE/v1/runs/$RUN_ID/events"
```

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

Local persistence is stored under `storage.local_dir`:

```text
data/runs/<run_id>/
  metadata.json
  messages.jsonl
  events.jsonl
```

`messages.jsonl` and `events.jsonl` are append-only JSON Lines files.

## Current Scope

Implemented:

- Standalone Go service
- Durable Runs
- Multi-Turn execution
- Local Store
- HTTP JSON API
- SSE Turn Event Stream
- Fake Model
- OpenAI-Compatible Provider
- Workspace Tools
- Shell Tool with conservative safety policy

Out of scope for this version:

- Frontend UI
- Docker packaging
- Database-backed Run Store
- MCP integration
- Full Plan Mode approval workflow
- Production context compaction
- Deep subagent recursion
- Fornax trace backend integration
- Full shell AST security analysis
