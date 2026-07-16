# Structured Observability and Token Streaming Design

## Goal

Make an executing Turn observable as structured, correlatable events and send
OpenAI-compatible model output to SSE clients as it arrives.

## Scope

- Preserve the existing Run Store event stream and SSE endpoint.
- Add event, Run, Turn, model-call, and tool-call identifiers where applicable.
- Add a structured `data` payload to events. Text chunks use
  `model_output_delta` with `data.delta`.
- Add an optional streaming capability to model providers. The OpenAI-compatible
  provider uses `stream: true` and reconstructs the final assistant message,
  including fragmented tool-call arguments.
- Persist text deltas through the existing event path and display them in the
  Web Console while the request remains open.

## Non-goals

- No external trace backend or Fornax integration.
- No event retention, aggregation, sampling, or query redesign.
- No model-summarised context memory or tool-result artifact storage.

## Design

`model.Provider` remains the synchronous compatibility boundary. Providers that
support streaming additionally implement `model.StreamingProvider`. The Runner
uses that capability only for the existing streaming Turn endpoint; ordinary
Turn requests retain the current full-response behavior.

The Server generates a Turn ID once per execution and stamps every persisted
event with a cryptographically random event ID and its Run ID. The Runner adds
the Turn ID to its events, generates a model-call ID for each model loop, and
associates tool lifecycle events with the requested tool-call ID. This leaves
Run-level events, such as `run_created`, correctly unassociated with a Turn.

The OpenAI-compatible stream reader processes SSE `data:` records incrementally.
It forwards text deltas immediately, accumulates final text, and joins partial
tool-call fields by their provider-supplied index before returning the normal
`model.Response` to the agent loop. Thus tool execution semantics and the final
persisted assistant message remain unchanged.

## Verification

- Provider tests cover streamed text and fragmented tool-call reconstruction.
- Server tests cover live `model_output_delta` SSE events, their persistence,
  and correlation fields.
