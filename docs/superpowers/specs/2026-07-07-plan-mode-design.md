# Plan Mode Design

## Goal

Implement Plan Mode for the Agent Service as a Run-level workflow: read-only planning first, explicit user confirmation second, execution with step progress third.

## Success Criteria

- A Run can enter Plan Mode through API or Web Console.
- Planning Turns restrict tool access to read-only exploration.
- Assistant plans are extracted from `Plan:` numbered sections and persisted.
- Users can execute or reset the persisted plan.
- Execution Turns restore normal tool access and track `[DONE:n]` completion markers.
- Plan State survives reloads because it is stored through the Run Store.
- SSE and persisted events expose Plan Mode transitions.
- Tests run without network access through the Fake Model.

## Architecture

Plan Mode is owned by the Run, not by the process or browser. The HTTP layer loads Plan State from the Run Store before each Turn and saves any updated Plan State after the Runner completes. The Runner receives Plan State as part of `TurnInput` and returns the updated state in `TurnOutput`.

The runtime uses `PlanModeMiddleware` to inject prompt context and restrict tool definitions. While planning, the model sees only read-only tools. While executing, it sees the persisted remaining steps and is instructed to emit `[DONE:n]` markers when work completes.

## Persistence

`RunStore` gains `GetPlan` and `SavePlan`. The Local Store writes `plan.json` next to `metadata.json`, `messages.jsonl`, and `events.jsonl`.

Run metadata includes lightweight plan summary fields so Run history can show planning status without loading every `plan.json`.

## API

- `GET /v1/runs/{run_id}/plan` returns the current Plan State.
- `POST /v1/runs/{run_id}/plan-mode` enables or disables planning.
- `POST /v1/runs/{run_id}/plan/execute` starts execution from a persisted plan.
- `POST /v1/runs/{run_id}/plan/reset` clears the persisted plan.
- `POST /v1/runs` accepts `plan_mode: true` for creating a Run directly in planning mode.

## Web Console

The console adds a Plan Mode toggle and a Plan panel. The panel shows mode, steps, completion progress, and actions for executing or resetting a plan.

## Testing

Focused tests cover plan parsing, local persistence, HTTP plan endpoints, planning tool restriction, execution progress tracking, and SSE/event persistence where practical.
