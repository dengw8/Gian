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

**Run Store**:
The persistence boundary for saving and loading Run state and Event Stream data without coupling the agent runtime to a concrete storage technology.
_Avoid_: database layer, file logger

**Local Store**:
The first Run Store implementation, backed by local files on disk.
_Avoid_: embedded database, production database

**Event Stream**:
The ordered sequence of runtime events emitted during a Run so clients can observe model output, tool calls, tool results, and termination.
_Avoid_: log stream, callback trace

**Model Provider**:
The component behind the agent runtime that turns messages and available tools into the next assistant response, either through a deterministic fake implementation or a configured real model endpoint.
_Avoid_: LLM client, model backend

**Fake Model**:
A deterministic Model Provider used for local development and tests without network access or credentials.
_Avoid_: mock server, dummy LLM

**OpenAI-Compatible Provider**:
A real Model Provider that speaks the OpenAI Chat Completions-compatible request and response protocol.
_Avoid_: multi-provider adapter, Responses API provider

**Tool**:
A capability exposed to the agent runtime with a name, input schema, safety attributes, and an execution function.
_Avoid_: plugin, command

**Workspace Tool**:
A Tool that reads or inspects files under the configured service workspace.
_Avoid_: filesystem plugin

**Shell Tool**:
A Tool that executes approved shell commands under the configured service workspace.
_Avoid_: terminal access, bash plugin
