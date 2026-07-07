# Agent Service Design

## Goal

Build a standalone Go Agent Service in this repository. The service must follow the architecture described in the referenced "Agent 工程架构" document: a thin ReAct runtime, middleware lifecycle hooks, tool safety metadata, observable event streams, durable Run state, and replaceable model and storage boundaries.

## Success Criteria

- The repository contains a new independent Go service at the root, not inside the `reference/` submodules.
- The service exposes HTTP JSON endpoints and an SSE endpoint for streaming Turn events.
- A Run is durable and supports multiple Turns.
- Run state is persisted through a `RunStore` interface with a first local-file implementation.
- The agent runtime has a real ReAct loop, six middleware lifecycle hooks, a shared State, a tool registry, and tool safety attributes.
- The model boundary supports deterministic fake, OpenAI Chat Completions-compatible, and ModelHub providers selected by configuration.
- The first tool set includes `read_file`, `list_files`, and `shell`.
- Tests can run without network access by using the fake model.

## Non-Goals

- Do not modify `reference/pi` or `reference/deer-flow`.
- Do not add Docker packaging in the first version.
- Do not implement database persistence in the first version.
- Do not fully implement MCP, production context compaction, deep subagent recursion, Fornax trace integration, or shell AST security analysis. These must have clear runtime extension points where relevant.

## Architecture

Use a thin Go runtime with explicit ports:

```text
cmd/agent-service
internal/config
internal/server
internal/agent
internal/model
internal/tools
internal/middleware
internal/store
internal/store/local
```

The dependency direction is:

```text
server -> agent -> model/tools/store interfaces
cmd/agent-service -> concrete config, model, tools, store, server assembly
```

The agent runtime must not depend on the local store implementation or the OpenAI-compatible model implementation directly. Concrete dependencies are assembled in `main`.

## Core Domain Objects

- `Run`: a durable conversation container that can receive multiple user Turns.
- `Turn`: one user message plus the agent work caused by that message.
- `Message`: a persisted user, assistant, or tool result message.
- `Event`: a structured observable runtime event.
- `ToolCall`: a model request to execute a named Tool with JSON arguments.
- `ToolResult`: a Tool output or Tool error.

## HTTP API

- `POST /v1/runs`
  - Creates a Run.
  - If `message` or `attachments` are provided, immediately executes the first Turn.
- `GET /v1/runs`
  - Returns persisted Run metadata, newest updated first.
- `GET /v1/runs/{run_id}`
  - Returns Run metadata.
- `GET /v1/runs/{run_id}/messages`
  - Returns persisted messages.
- `GET /v1/runs/{run_id}/events`
  - Returns persisted events.
- `POST /v1/runs/{run_id}/turns`
  - Appends one user message, optionally with inline image attachments, and executes a non-streaming Turn.
- `POST /v1/runs/{run_id}/turns/stream`
  - Appends one user message, optionally with inline image attachments, and streams this Turn's events as SSE.
- `GET /v1/runs/{run_id}/plan`
  - Returns persisted Plan State.
- `POST /v1/runs/{run_id}/plan-mode`
  - Enables or disables read-only planning for a Run.
- `POST /v1/runs/{run_id}/plan/execute`
  - Starts executing the persisted plan.
- `POST /v1/runs/{run_id}/plan/reset`
  - Clears the persisted plan.

The business assumption is that the same Run will not receive concurrent Turn submissions. The implementation should still keep a lightweight `running` status check and return `409 Conflict` for obvious concurrent misuse.

## Persistence

Define a `RunStore` interface for persistence. The first implementation stores each Run under a local directory:

```text
data/runs/<run_id>/
  metadata.json
  plan.json
  messages.jsonl
  events.jsonl
```

The local store appends messages and events as JSON Lines. Metadata contains Run status, timestamps, model configuration snapshot, and error information. The store boundary must be designed so a future database-backed implementation can replace local files without changing the agent runtime.

Messages include `created_at` timestamps. User messages may include inline image attachments as base64 data plus media type metadata. The local JSONL reader uses a streaming JSON decoder so large attachment lines can be read back during later Turns.

Token usage is persisted per model call on `model_completed` events and cumulatively on Run metadata. Usage fields are provider-reported and optional; providers that do not return token accounting leave those fields absent.

## Configuration

Use YAML for non-secret configuration and an environment variable for the model API key:

- Default config path: `config.yaml`
- Override config path: `AGENT_CONFIG`
- API key override: `AGENT_MODEL_API_KEY`
- ModelHub AK override: `AGENT_MODELHUB_AK`

Expected settings:

```yaml
server:
  addr: ":8080"
storage:
  local_dir: "data"
model:
  provider: "fake"
  openai:
    base_url: "https://api.openai.com/v1"
    model: "gpt-4.1-mini"
  modelhub:
    endpoint: "https://aidp.bytedance.net/api/modelhub/online/v2/crawl"
    model: "gpt-5.4-2026-03-05"
    max_tokens: 500
```

If the configured provider is `fake`, the service uses the deterministic fake model. If the provider is `openai_compatible`, the service requires `base_url`, `model`, and an API key. If the provider is `modelhub`, the service requires `endpoint`, `model`, and an AK from either environment or ignored local config.

## Agent Runtime

The runtime follows the ReAct loop from the document:

1. `BeforeAgent`: initialize Run and Turn state.
2. For each iteration until convergence or max iterations:
   - Build model request from system prompt, messages, and registered tools.
   - Run `BeforeModel`.
   - Call `ModelProvider.Generate`.
   - Append assistant message.
   - Run `AfterModel`.
   - If there are no tool calls, complete the Turn.
   - For each tool call:
     - Run `BeforeTool`.
     - Execute the Tool.
     - Append tool result.
     - Run `AfterTool`.
3. `AfterAgent`: finalize metadata and emit terminal events.

The runtime includes a max iteration guard to prevent runaway loops.

## Middleware

Define the six lifecycle hooks described in the source document:

```go
type Middleware interface {
    Name() string
    BeforeAgent(ctx context.Context, state *State) error
    BeforeModel(ctx context.Context, req *ModelRequest, state *State) error
    AfterModel(ctx context.Context, resp *ModelResponse, state *State) error
    BeforeTool(ctx context.Context, call ToolCall, state *State) error
    AfterTool(ctx context.Context, result ToolResult, state *State) error
    AfterAgent(ctx context.Context, state *State) error
}
```

First-version middleware:

- Event persistence middleware: emits and persists structured events.
- Safety middleware: checks Tool safety attributes and shell command policy.
- System prompt middleware: builds static and dynamic sections.
- Plan Mode middleware: injects planning/execution context and restricts tools during read-only planning.
- No-op extension middlewares for future compaction and subagent support where they clarify the boundary.

## State

`State` is the runtime working memory:

```go
type State struct {
    Messages []Message
    Metadata map[string]any
}
```

The first implementation may keep state single-threaded within one Turn. It should avoid global mutable state so future concurrent Runs remain straightforward.

## Tools

Define a Tool interface with safety attributes:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    ReadOnly() bool
    Destructive() bool
    Concurrent() bool
    Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
}
```

First tool set:

- `read_file`: reads a file under the configured workspace.
- `list_files`: lists files under the configured workspace.
- `shell`: executes approved shell commands under the configured workspace.

The shell tool starts with a conservative deny policy for destructive commands and command chaining. A future shell analyzer can replace this policy behind the safety boundary.

## Model Providers

Define a `ModelProvider` interface:

```go
type ModelProvider interface {
    Generate(ctx context.Context, req ModelRequest) (ModelResponse, error)
}
```

Implementations:

- `fake`: deterministic provider used by tests and local development.
- `openai_compatible`: calls an OpenAI Chat Completions-compatible endpoint using `base_url`, `model`, and API key.
- `modelhub`: calls the ByteDance ModelHub crawl endpoint using `endpoint`, `model`, `max_tokens`, and AK.

The OpenAI-compatible implementation maps internal messages and tools to Chat Completions request fields and maps assistant text/tool calls back to internal response types.

The ModelHub implementation maps text and image attachments to content parts. Image attachments are sent as `image_url` data URLs. `X-TT-LOGID` is generated per request unless a test injects one.

## Web Console

The service embeds a static Web Console under `/app/`. It supports:

- Creating empty Runs.
- Loading persisted Run history from `GET /v1/runs`.
- Loading Run metadata, messages, and events.
- Sending non-streaming and SSE Turns.
- Optimistically displaying user messages while requests are in flight.
- Attaching, previewing, sending, and rendering inline images.
- Enabling Plan Mode, viewing persisted Plan Steps, executing a plan, and resetting Plan State.

The Web Console is served by the Go service and has no separate Node build step.

## System Prompt

Implement a `SystemPromptBuilder` that separates static and dynamic sections:

- Static: role, safety rules, behavior rules, tool-use policy, output policy.
- Dynamic: runtime environment, configured tools, project instructions if present, Plan Mode context, and other per-Run/per-Turn state.

The builder emits a clear static/dynamic boundary so stable content remains at the front of the prompt for provider prompt-cache reuse. More stable dynamic sections are ordered before more volatile sections.

## Observability

Emit structured events for:

- Run creation.
- Turn start.
- Model start and completion.
- Tool start and completion.
- Turn completion.
- Turn failure.

The non-streaming API returns the final Run/Turn result and can include relevant events. The streaming API sends these events as SSE while also persisting them to the Run Store.

`model_completed` events include token usage when the provider returns it. The Run metadata stores cumulative totals for prompt, completion, total, cached, and reasoning tokens when available.

## Testing

Run `go test ./...`.

Focused tests:

- Fake model completes a Turn without tool calls.
- Fake model requests a tool; runtime executes it and appends tool result.
- Multi-turn Run loads prior messages before continuing.
- Local store writes metadata, messages, and events.
- Local store reads large message JSONL entries created by image attachments.
- Store implementations can run the shared Run Store contract test suite.
- Dangerous shell commands are rejected.
- HTTP handlers create Runs, append Turns, and stream SSE events.
- HTTP handlers persist and return message timestamps and image attachments.
- ModelHub request mapping tests cover text, tool calls, and image attachments.
- Plan Mode tests cover plan parsing, read-only tool restriction, plan APIs, and persisted completion progress.
- Token usage tests cover provider parsing, Run metadata accumulation, and per-call event persistence.

## Delivery Files

First version should include:

- `go.mod`
- `cmd/agent-service/main.go`
- `internal/...` packages described above
- `config.example.yaml`
- embedded `internal/server/web` assets
- `Makefile` with `make test` and `make run`
- `README.md` with local startup and API examples

## Open Decisions

No open design decisions remain for the current implementation pass. Future work includes database Run Store, context compaction, subagent delegation, MCP support, full command AST analysis, trace backend integration, and Docker packaging.
