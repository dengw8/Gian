# Agent Service

This context defines the language for the standalone agent service being built in this repository.

## Language

**Agent Service**:
A standalone Go service that runs an agent loop, exposes service endpoints, and owns the agent runtime architecture for this repository.
_Avoid_: TypeScript service, reference submodule modification

**Reference Project**:
An external project checked out under `reference/` and used only for architectural comparison or implementation examples.
_Avoid_: source service, implementation target

**Run**:
A durable conversation container that can receive multiple user turns and repeatedly advance the agent runtime over its persisted messages and events.
_Avoid_: job, one-shot task, transient request

**Turn**:
One user message appended to a Run plus the agent work that follows from that message.
_Avoid_: request, step, iteration

**Message**:
A persisted user, assistant, or tool message in a Run. Messages can include text content, tool calls, tool results, timestamps, and attachments.
_Avoid_: chat bubble, raw payload

**Attachment**:
Inline binary user input attached to a Message. The current implementation supports image attachments persisted as base64 data with media type metadata.
_Avoid_: uploaded file, external asset

**Run Store**:
The persistence boundary for saving and loading Run state and Event Stream data without coupling the agent runtime to a concrete storage technology.
_Avoid_: database layer, file logger

**Local Store**:
The first Run Store implementation, backed by local files on disk.
_Avoid_: embedded database, production database

**Event Stream**:
The ordered sequence of runtime events emitted during a Run so clients can observe model output, tool calls, tool results, and termination.
_Avoid_: log stream, callback trace

**Plan Mode**:
A Run-level mode where the Agent first performs read-only exploration and produces a persisted plan, then waits for explicit execution before using normal tool access to carry out the plan.
_Avoid_: global service mode, transient UI toggle

**Plan State**:
The persisted planning state for a Run, including the current mode, planned steps, and progress timestamps.
_Avoid_: frontend-only state, prompt-only state

**Plan Step**:
One numbered item in a Plan State. A Plan Step moves from pending to completed as execution messages report completion.
_Avoid_: issue, task tracker ticket

**Model Provider**:
The component behind the agent runtime that turns messages and available tools into the next assistant response, either through a deterministic fake implementation or a configured real model endpoint.
_Avoid_: LLM client, model backend

**Fake Model**:
A deterministic Model Provider used for local development and tests without network access or credentials.
_Avoid_: mock server, dummy LLM

**OpenAI-Compatible Provider**:
A real Model Provider that speaks the OpenAI Chat Completions-compatible request and response protocol.
_Avoid_: multi-provider adapter, Responses API provider

**ModelHub Provider**:
A real Model Provider for the ByteDance ModelHub crawl endpoint. It maps text and image attachments into ModelHub content parts and sends a generated request log id.
_Avoid_: OpenAI-compatible provider, generic HTTP client

**Web Console**:
The embedded browser UI served from `/app/` for creating Runs, loading Run history, sending Turns, attaching images, and inspecting messages and events.
_Avoid_: marketing site, separate frontend service

**Tool**:
A capability exposed to the agent runtime with a name, input schema, safety attributes, and an execution function.
_Avoid_: plugin, command

**Workspace Tool**:
A Tool that reads or inspects files under the configured service workspace.
_Avoid_: filesystem plugin

**Shell Tool**:
A Tool that executes approved shell commands under the configured service workspace.
_Avoid_: terminal access, bash plugin
