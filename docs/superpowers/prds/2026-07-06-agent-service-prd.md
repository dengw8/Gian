# Build the Agent Service

## Problem Statement

The repository currently contains reference projects but no standalone Agent Service implementation. The user needs a new Go service that turns the architecture described in the "Agent 工程架构" document into a runnable, testable service owned by this repository.

The problem is not only to call a model. The service needs a durable Run model, multi-Turn conversations, structured Event Stream observability, Tool execution, safety metadata, replaceable Model Provider integration, and a persistence boundary that can start with local files while remaining open to database-backed storage later.

## Solution

Build a standalone Go Agent Service at the repository root. The service exposes HTTP JSON endpoints and an SSE endpoint for streaming Turn events. It runs a ReAct-style agent loop with a thin runtime, six middleware lifecycle hooks, a shared State, Tool Registry, Tool safety attributes, durable Run persistence, and configurable Model Providers.

The first release uses a Fake Model for deterministic development and tests. It also supports an OpenAI-Compatible Provider when configuration supplies a base URL, model name, and API key. The first Run Store implementation is a Local Store that persists metadata, messages, and events to local files. The Run Store must be accessed through an interface so future storage implementations can be added without rewriting the Agent runtime.

## User Stories

1. As a developer, I want a standalone Go Agent Service in this repository, so that the implementation is owned here rather than hidden inside a Reference Project.
2. As a developer, I want the Reference Projects to remain untouched, so that they stay useful as external comparison material.
3. As a service consumer, I want to create a Run through an HTTP API, so that I can start a durable agent conversation.
4. As a service consumer, I want a Run to support multiple Turns, so that I can continue a conversation over persisted history.
5. As a service consumer, I want to append a Turn to an existing Run, so that each user message advances the same durable conversation.
6. As a service consumer, I want to fetch Run metadata, so that I can inspect status, timestamps, model configuration, and error state.
7. As a service consumer, I want to fetch persisted messages for a Run, so that I can inspect the conversation state.
8. As a service consumer, I want to fetch persisted events for a Run, so that I can debug what happened during execution.
9. As a service consumer, I want a non-streaming Turn endpoint, so that simple clients can submit a message and wait for the final result.
10. As a service consumer, I want a streaming Turn endpoint over SSE, so that clients can observe model and Tool progress as it happens.
11. As a service consumer, I want structured Event Stream events, so that UI, debugging, and tracing consumers do not need to parse free-form logs.
12. As a service consumer, I want Turn failure events to be persisted, so that failures remain inspectable after the HTTP request ends.
13. As a service operator, I want the Agent Service to load YAML configuration, so that local setup is straightforward.
14. As a service operator, I want model API keys to come from environment variables, so that credentials are not committed to the repository.
15. As a service operator, I want the service to run with a Fake Model by default, so that the service works without network credentials.
16. As a service operator, I want to configure an OpenAI-Compatible Provider, so that the service can call a real model endpoint when credentials are available.
17. As a developer, I want the Model Provider behind an interface, so that Fake Model and OpenAI-Compatible Provider can be swapped without changing the Agent runtime.
18. As a developer, I want the Run Store behind an interface, so that Local Store can later be replaced by a database-backed store.
19. As a developer, I want a Local Store, so that Runs can persist without external infrastructure.
20. As a developer, I want Local Store files grouped by Run, so that persisted metadata, messages, and events are easy to inspect.
21. As a developer, I want messages persisted as JSON Lines, so that appending Turn data is simple and inspectable.
22. As a developer, I want events persisted as JSON Lines, so that the Event Stream can be replayed or inspected after execution.
23. As a developer, I want a real ReAct loop, so that the Agent runtime can reason, act through Tools, observe Tool results, and converge.
24. As a developer, I want a max-iteration guard, so that a Run cannot spin forever when the model repeatedly requests Tool calls.
25. As a developer, I want the Agent runtime to use a shared State, so that middleware and the loop operate over a clear working memory.
26. As a developer, I want six middleware lifecycle hooks, so that capabilities can attach to the runtime without expanding the core loop.
27. As a developer, I want `BeforeAgent` and `AfterAgent` hooks, so that Run and Turn boundaries can be observed and finalized.
28. As a developer, I want `BeforeModel` and `AfterModel` hooks, so that prompt construction, context management, and model result observation have stable extension points.
29. As a developer, I want `BeforeTool` and `AfterTool` hooks, so that safety checks and Tool result handling have stable extension points.
30. As a developer, I want a Tool Registry, so that Tools are registered and looked up by name rather than hard-coded into the loop.
31. As a developer, I want each Tool to declare safety attributes, so that runtime policy can reason about read-only, destructive, and concurrent behavior.
32. As a developer, I want a `read_file` Tool, so that the Agent can inspect workspace files.
33. As a developer, I want a `list_files` Tool, so that the Agent can discover workspace files.
34. As a developer, I want a Shell Tool, so that the Agent can run approved local commands.
35. As a service operator, I want dangerous shell commands rejected, so that the first service does not expose obvious destructive behavior.
36. As a developer, I want the shell safety policy behind a clear boundary, so that a future AST-based command analyzer can replace the first conservative policy.
37. As a developer, I want a System Prompt Builder with static and dynamic sections, so that future prompt-cache optimization and context injection have the right shape.
38. As a developer, I want Plan Mode middleware plus extension points for compaction and subagent support, so that the architecture follows the source document without forcing every broader capability immediately.
39. As a developer, I want the HTTP API tested from the outside, so that user-visible behavior remains stable while internals evolve.
40. As a developer, I want the Agent runtime tested with a Fake Model, so that ReAct behavior is deterministic and does not require network access.
41. As a developer, I want Tool safety tested narrowly, so that dangerous command rejection remains explicit and cheap to verify.
42. As a developer, I want `make test`, so that the test suite has a simple project-level entrypoint.
43. As a developer, I want `make run`, so that the service has a simple local startup path.
44. As a developer, I want a sample configuration file, so that local setup is discoverable.
45. As a developer, I want README startup and API examples, so that the first service can be exercised without reading the code.

## Implementation Decisions

- Build a new independent Go service at the repository root.
- Do not modify any Reference Project.
- Use a thin runtime with explicit ports rather than a framework-heavy service or graph workflow engine.
- Split the service into packages for startup, configuration, HTTP server, Agent runtime, Model Providers, Tools, middleware, Run Store interface, and Local Store implementation.
- Keep dependency direction clean: the HTTP layer calls the Agent runtime; the Agent runtime depends on Model Provider, Tool, and Run Store interfaces; concrete dependencies are assembled at startup.
- Expose durable Runs and append-only Turns instead of treating every request as an isolated execution.
- Support both non-streaming and SSE streaming Turn execution.
- Persist Run metadata, messages, and Event Stream data.
- Store local Run data under a Run-specific directory with metadata JSON plus messages and events as JSON Lines.
- Use YAML configuration for non-secret service settings.
- Use an environment variable override for the model API key.
- Support `fake` and `openai_compatible` Model Provider modes.
- Use OpenAI Chat Completions-compatible request and response semantics for the real provider.
- Implement a ReAct loop that calls the model, executes model-requested Tools, appends Tool results, and continues until convergence or max iterations.
- Implement the six middleware lifecycle hooks from the source architecture.
- Implement State as messages plus metadata.
- Implement Tool safety attributes for read-only, destructive, and concurrent behavior.
- Implement `read_file`, `list_files`, and `shell` as the first Tool set.
- Keep the first shell safety implementation conservative, rejecting destructive commands and command chaining.
- Include event persistence and safety as real middleware.
- Include system prompt construction with static and dynamic sections.
- Implement Run-level Plan Mode while keeping context compaction and subagent support as explicit extension points or minimal no-op middleware.
- Treat same-Run concurrent Turn submission as outside the business flow, but keep a lightweight `running` status check to reject obvious misuse.
- Do not add Docker packaging in the first release.

## Testing Decisions

- Good tests should verify external behavior and stable contracts rather than internal implementation details.
- The primary test seam is the HTTP API: create Runs, append Turns, fetch messages, fetch events, and stream Turn events over SSE.
- The secondary test seam is the Agent runtime with a Fake Model and a test Run Store: verify ReAct convergence, Tool execution, Tool result feedback, Event Stream emission, max iteration behavior, and multi-Turn context loading.
- The narrow security seam is Tool safety policy: verify dangerous shell commands are rejected without needing a full HTTP setup for every policy case.
- Tests must run without network access by using the Fake Model.
- Local Store tests should verify metadata, messages, and events are written in the expected durable shape.
- OpenAI-Compatible Provider tests should avoid real network calls and use an HTTP test server or request mapping tests.
- HTTP tests should cover non-streaming and SSE streaming paths.
- Multi-Turn tests should prove that an existing Run's prior messages are loaded before the next Turn advances the Agent runtime.
- Safety tests should prove destructive or chained shell commands do not execute.

## Out of Scope

- Frontend UI.
- Docker packaging.
- Database-backed Run Store.
- Production context compaction.
- MCP integration.
- Deep subagent recursion.
- Fornax trace backend integration.
- Full shell AST security analysis.
- Direct edits to `reference/pi` or `reference/deer-flow`.
- Treating external PRs as a triage input for this feature.

## Further Notes

- The PRD follows the repository glossary in `CONTEXT.md`.
- The PRD respects the ADRs for OpenAI-compatible model integration, Run Store abstraction with Local Store first, Run and Turn HTTP API shape, and YAML configuration with environment-based secret override.
- The implementation should be split into independently grabbable issues before coding because the feature spans runtime architecture, persistence, HTTP API, model integration, Tools, safety, and documentation.

## Implementation Updates

The implementation later grew beyond the original first-release scope in a few practical ways:

- Added an embedded Web Console served from `/app/`.
- Added `GET /v1/runs` so the Web Console can load persisted Run history.
- Added per-message `created_at` timestamps.
- Added inline image attachments on user messages, persisted in `messages.jsonl`.
- Added ModelHub Provider support for the ByteDance ModelHub crawl endpoint.
- Added ModelHub image mapping using `image_url` data URLs.
- Added a reusable Run Store contract test suite for current and future store implementations.
- Added local `config.yaml` support for ModelHub AK, with `config.yaml` ignored by git.
- Added Run-level Plan Mode with read-only planning, explicit execution, persisted Plan State, plan APIs, Web Console controls, and completion tracking.
- Reworked System Prompt construction around static and dynamic sections for prompt-cache friendliness.
- Added provider token usage collection, including cached token counts when returned by the model API.

The following items remain future work from the broader architecture:

- Database-backed Run Store.
- Production context compaction.
- Subagent delegation beyond no-op extension middleware.
- MCP integration.
- Fornax or other trace backend integration.
- Full shell AST security analysis.
- Docker packaging.
