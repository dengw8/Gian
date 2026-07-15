# Persistent Shell Approval Design

## Goal

Add a durable confirmation workflow for high-risk Shell tool calls. The workflow
keeps PI's user-facing semantic: one Shell tool call contains one complete
script and requires one approve-or-deny decision. Unlike PI's in-memory
terminal UI flow, the Agent Service persists the waiting state so a browser
disconnect or service restart does not lose the decision.

## Success Criteria

- A POSIX Shell script that is demonstrably read-only executes without
  confirmation.
- A script containing a high-risk or unclassifiable operation does not execute
  until the user explicitly approves it.
- The confirmation UI displays the complete immutable script, workspace, and
  risk reasons.
- The decision endpoint accepts only `approve`, `deny`, or `cancel`; it never
  accepts a replacement script.
- Approval executes exactly the persisted script after its hash and risk
  classification are revalidated.
- Denial and cancellation append a tool result and automatically resume the
  same Turn so the model can choose another action.
- A Run awaiting confirmation survives Local Store and MySQL Store reloads,
  rejects ordinary new Turns, and cannot execute twice after duplicate
  approval requests.
- Existing low-risk Shell behavior and all non-Shell tools remain unchanged.

## Non-goals

- Approval batches spanning multiple Shell tool calls.
- Approval expiration, approval caches, or cross-call authorization reuse.
- A generic approval framework for every Tool.
- P1 token streaming or broader Event Stream redesign.
- MCP, Memory, and subagent capabilities.

## Current Constraints

- The Shell Tool executes `/bin/sh -c` and currently rejects shell control
  operators with string matching.
- The Runner executes tool calls sequentially and represents all `BeforeTool`
  failures as tool errors.
- Runs persist append-only Messages, Events, and Model I/O Logs, plus optional
  Plan and Compaction state.
- The Local Store and MySQL Store implement the same `RunStore` contract.
- PI Core itself waits for an interactive `beforeToolCall` hook in memory; it
  does not persist permission state. This design adapts that interaction for a
  durable HTTP service.

## Architecture

### Shell Risk Analysis

Add a Shell-risk analysis component dedicated to the Shell Tool. It parses the
script using `mvdan.cc/sh/v3/syntax` in POSIX mode, matching the `/bin/sh`
execution dialect.

The analyzer returns one of:

- `allow`: every parsed operation is known to be read-only;
- `approval_required`: at least one operation is known high-risk or cannot be
  classified safely;
- `invalid`: the script cannot be parsed or violates the accepted Shell
  dialect.

Unknown commands, redirects, command substitutions, assignments with command
execution, network-facing commands, and operations with possible filesystem or
shared-system side effects are classified as `approval_required`. This is a
deliberately conservative default. The policy records the matching risk reasons
for display and audit.

The existing raw-token blacklist is removed only after the parser-backed policy
covers its safety constraints. A single Shell Tool call may then contain a
complete POSIX script. The approval boundary is that whole script, not each
individual shell statement.

### Runner Pause Boundary

`ShellApprovalMiddleware` runs at the existing `BeforeTool` lifecycle point.
For non-Shell calls it is a no-op. When the analyzer returns
`approval_required`, the middleware returns a typed internal approval-required
signal rather than a normal tool-execution error.

The Runner recognizes that signal and produces a structured paused Turn output.
It preserves the assistant Message that created the tool call, all earlier
tool results in the same assistant response, and its Model I/O Logs. It does
not execute the pending Shell script or subsequent tool calls from that
assistant response.

The paused output includes a `PendingShellApproval` value:

```go
type PendingShellApproval struct {
    ID                 string
    Version            int
    ToolCall           runtime.ToolCall
    Workspace          string
    RiskReasons        []string
    ScriptHash         string
    RemainingToolCalls []runtime.ToolCall
    RequestedAt        time.Time
    ResolvedAt         time.Time
    Status             ShellApprovalStatus
}
```

Only one pending Shell approval may exist for a Run. A later high-risk Shell
call creates a new approval after the preceding approval has resolved and the
Turn resumes.

### Persistence

Add `awaiting_approval` to `RunStatus`.

Extend `RunStore` with operations to load a pending Shell approval, save it,
and atomically resolve it using the approval ID and expected version. An atomic
resolution transitions a `pending` approval to `executing`; only that caller
may execute the script. The final outcome clears the pending state and updates
the Run status.

Local Store persists the state in:

```text
data/runs/<run_id>/pending_approval.json
```

MySQL adds a nullable `pending_approval_payload LONGTEXT` column to
`agent_run_states`. The schema migration is
`migrations/mysql/002_add_pending_shell_approval.sql`; it is operator-applied,
consistent with the existing MySQL Run Store migration model. No new approval
table is needed because a Run has at most one pending Shell approval.

Messages, Events, and Model I/O Logs continue to use their existing append-only
tables/files. The service emits `approval_required`, `approval_approved`,
`approval_denied`, and `approval_cancelled` Events.

### HTTP and Web Console

Expose the current pending approval through a read endpoint and resolve it via:

```text
POST /v1/runs/{runID}/approvals/{approvalID}
{ "decision": "approve" | "deny" | "cancel" }
```

The request contains no script content. Request decoding rejects unknown
fields, including replacement script data. The server loads the persisted
record, verifies its `pending` status, ID, and version, then acts on the stored
Tool call only.

The Web Console listens for `approval_required` and renders the full script,
workspace, and risk reasons. It provides Approve, Deny, and Cancel controls.

While a Run is `awaiting_approval`, ordinary Turn endpoints return conflict.
There is intentionally no expiration: the approval remains pending until an
explicit `approve`, `deny`, or `cancel` decision.

### Resolution and Resume

On approval, the service verifies the persisted script hash and reruns the
parser-backed classification. A mismatch or a stricter current classification
prevents execution and requires a new approval. The Shell Tool receives the
stored script exactly as displayed.

On denial or cancellation, the service creates an error Tool Result describing
the user's decision. On approval it executes the stored Shell script and
creates the normal Tool Result.

All three outcomes resume the same Turn without a new user Message:

1. append the resolved Tool Result;
2. execute any `RemainingToolCalls` in their original order;
3. if no new approval is needed, continue the model loop.

If a remaining Shell call requires approval, the Runner pauses again with a new
pending approval. This keeps the behavior aligned with PI's per-tool-call
confirmation model while remaining durable.

## Error Handling

- Invalid scripts return a normal error Tool Result; they never become
  approvable scripts.
- Unknown approval IDs, non-pending approvals, stale versions, and duplicate
  resolutions return conflict and never execute a script.
- A failed approved Shell execution becomes an error Tool Result and the model
  receives it on resume.
- A storage failure before the pending state is durable fails the Turn without
  executing the script.
- Resolution uses a compare-and-swap transition on approval ID, version, and
  status. Repeated or racing decisions cannot execute a script twice.
- If the process restarts after the approval has transitioned to `executing`,
  the service never retries that script automatically. Recovery marks the Run
  failed/recovery-required and records an event for operator inspection. This
  at-most-once policy avoids duplicating an external side effect when it is
  unknown whether the shell process ran.

## Testing

### Agent

- Read-only scripts execute directly.
- High-risk and unknown scripts produce paused output and do not execute.
- The complete assistant tool-call Message and earlier results are retained at
  pause time.
- Approve, deny, and cancel produce the expected Tool Result and resume the
  remaining calls in order.
- A second high-risk Shell call pauses again after the preceding resolution.

### Store

- Local and MySQL implementations save, load, and clear pending approvals.
- Atomic resolution accepts one matching ID/version and rejects duplicate or
  stale decisions.
- Reloading a Run restores its pending state.

### Server and Web Console

- Waiting Runs reject ordinary Turn requests.
- Approval endpoints reject replacement script data and invalid decisions.
- Events and Model I/O Logs record pause and resolution.
- The console renders complete script content and submits only the fixed
  decision values.

## Files Expected to Change

- `internal/runtime/types.go`
- `internal/agent/agent.go`
- `internal/agent/middleware.go`
- `internal/agent/safety.go` and new Shell approval/risk-analysis files
- `internal/tools/shell.go`
- `internal/store/store.go`
- `internal/store/local/store.go`
- `internal/store/mysql/models.go`
- `internal/store/mysql/store.go`
- `migrations/mysql/002_add_pending_shell_approval.sql`
- `internal/server/server.go`
- `internal/server/web/`
- Focused tests in the affected packages
- `go.mod` and `go.sum`
