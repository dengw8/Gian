# Agent Service Design

## Goal

Build a standalone Go Agent Service in this repository. The service must follow the architecture described in the referenced "Agent 工程架构" document: a thin ReAct runtime, middleware lifecycle hooks, tool safety metadata, observable event streams, durable Run state, and replaceable model and storage boundaries.

## Success Criteria

- The repository contains a new independent Go service at the root, not inside the `reference/` submodules.
- The service exposes HTTP JSON endpoints and an SSE endpoint for streaming Turn events.
- A Run is durable and supports multiple Turns.
- Run state is persisted through a `RunStore` interface with a first local-file implementation.
- The agent runtime has a real ReAct loop, six middleware lifecycle hooks, a shared State, a tool registry, and tool safety attributes.
- The model boundary supports a deterministic fake provider and an OpenAI Chat Completions-compatible provider selected by configuration.
- The first tool set includes `read_file`, `list_files`, and `shell`.
- Tests can run without network access by using the fake model.

## Non-Goals

- Do not modify `reference/pi` or `reference/deer-flow`.
- Do not implement a frontend.
- Do not add Docker packaging in the first version.
- Do not implement database persistence in the first version.
- Do not fully implement MCP, full Plan Mode approval flow, production context compaction, deep subagent recursion, Fornax trace integration, or shell AST security analysis. These must have clear runtime extension points where relevant.

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
  - If `message` is provided, immediately executes the first Turn.
- `GET /v1/runs/{run_id}`
  - Returns Run metadata.
- `GET /v1/runs/{run_id}/messages`
  - Returns persisted messages.
- `GET /v1/runs/{run_id}/events`
  - Returns persisted events.
- `POST /v1/runs/{run_id}/turns`
  - Appends one user message and executes a non-streaming Turn.
- `POST /v1/runs/{run_id}/turns/stream`
  - Appends one user message and streams this Turn's events as SSE.

The business assumption is that the same Run will not receive concurrent Turn submissions. The implementation should still keep a lightweight `running` status check and return `409 Conflict` for obvious concurrent misuse.

## Persistence

Define a `RunStore` interface for persistence. The first implementation stores each Run under a local directory:

```text
data/runs/<run_id>/
  metadata.json
  messages.jsonl
  events.jsonl
```

The local store appends messages and events as JSON Lines. Metadata contains Run status, timestamps, model configuration snapshot, and error information. The store boundary must be designed so a future database-backed implementation can replace local files without changing the agent runtime.

## Configuration

Use YAML for non-secret configuration and an environment variable for the model API key:

- Default config path: `config.yaml`
- Override config path: `AGENT_CONFIG`
- API key override: `AGENT_MODEL_API_KEY`

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
```

If the configured provider is `fake`, the service uses the deterministic fake model. If the provider is `openai_compatible`, the service requires `base_url`, `model`, and an API key.

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
- No-op extension middlewares for future Plan Mode, compaction, and subagent support where they clarify the boundary.

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

The OpenAI-compatible implementation maps internal messages and tools to Chat Completions request fields and maps assistant text/tool calls back to internal response types.

## System Prompt

Implement a `SystemPromptBuilder` that separates static and dynamic sections:

- Static: role, behavior rules, safety rules, output expectations.
- Dynamic: working directory, current time, configured tools, project instructions if present.

The first version does not implement prompt-cache measurement. It preserves the section boundary so cache-aware ordering can be refined later without changing runtime flow.

## Observability

Emit structured events for:

- Run creation.
- Turn start.
- Model start and completion.
- Tool start and completion.
- Turn completion.
- Turn failure.

The non-streaming API returns the final Run/Turn result and can include relevant events. The streaming API sends these events as SSE while also persisting them to the Run Store.

## Testing

Run `go test ./...`.

Focused tests:

- Fake model completes a Turn without tool calls.
- Fake model requests a tool; runtime executes it and appends tool result.
- Multi-turn Run loads prior messages before continuing.
- Local store writes metadata, messages, and events.
- Dangerous shell commands are rejected.
- HTTP handlers create Runs, append Turns, and stream SSE events.

## Delivery Files

First version should include:

- `go.mod`
- `cmd/agent-service/main.go`
- `internal/...` packages described above
- `config.example.yaml`
- `Makefile` with `make test` and `make run`
- `README.md` with local startup and API examples

## Open Decisions

No open design decisions remain for the first implementation pass. Future work includes database Run Store, richer Plan Mode, context compaction, subagent delegation, MCP support, full command AST analysis, and trace backend integration.
