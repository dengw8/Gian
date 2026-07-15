# Persistent Shell Approval — Implementation Plan

> Design: `docs/superpowers/specs/2026-07-15-persistent-shell-approval-design.md`

## Goal and acceptance criteria

Implement P0 Shell approval only. A complete Shell script is one approval
unit: demonstrably read-only scripts execute immediately; scripts containing a
high-risk or unknown construct pause the Run and are never executed before an
explicit `approve` decision. `deny` and `cancel` return a Tool error and also
resume the same Turn. The Run Store must retain enough data to reload and
resolve an outstanding approval without accepting client-provided shell input.

The implementation must preserve the agreed at-most-once recovery rule: an
approval found in `executing` after restart is failed/recovery-required rather
than automatically rerun.

## Scope and constraints

- Only the existing `shell` tool participates. No generic tool approval,
  expiry, approval reuse, authorization batch, or new Run-cancel API.
- One model tool call may contain a multi-command script and receives exactly
  one decision. Separate Shell tool calls can pause separately.
- The approval request body is exactly `{ "decision": "approve" | "deny" |
  "cancel" }`; unknown fields must be rejected.
- Maintain the current Local and MySQL Run Store backends and their shared
  contract tests. MySQL DDL remains operator-applied.

## Implementation steps

### 1. Replace string blacklist validation with parsed Shell risk analysis

**Files:** `go.mod`, `go.sum`, `internal/tools/shell.go`, new focused tests in
`internal/tools/`

1. Add `mvdan.cc/sh/v3/syntax` and parse with the POSIX variant.
2. Introduce a typed analysis result, for example `ShellRiskAnalysis`, with a
   disposition (`read_only` or `approval_required`) and stable risk reasons.
3. Keep invalid JSON and syntactically invalid scripts as ordinary Shell input
   errors; they are not approvable.
4. Conservatively prove read-only only for the existing allowlisted commands
   and their already-supported safe forms. Any assignment, expansion,
   redirection, compound construct, unsupported flag/form, or command not
   provably read-only is `approval_required`. This permits complete scripts in
   the tool while treating uncertainty as high risk.
5. Change `ShellTool.Execute` to validate syntax but not reject high-risk
   scripts; execution remains behind the approval middleware. Update its tool
   description and parameter description from a “single command” to a full
   script.
6. Keep `ValidateReadOnlyShellCommand` as the Plan Mode policy entry point,
   now backed by the parsed read-only classification.

**Tests:** parsed read-only commands execute; multi-command, destructive,
unknown, redirection, substitution, and malformed scripts classify correctly;
`ShellTool.Execute` rejects malformed input but can execute a syntactically
valid script when called after approval.

### 2. Add approval domain types and a typed Runner pause signal

**Files:** `internal/runtime/types.go`, `internal/agent/safety.go`,
`internal/agent/agent.go`, `internal/agent/middleware.go`,
`internal/agent/agent_test.go`

1. Define serializable approval state: ID, version, stored tool call,
   workspace, risk reasons, SHA-256 script hash, remaining calls, timestamps,
   and `pending`/`executing` status. Add `awaiting_approval` to `RunStatus`.
2. Replace the current `SafetyMiddleware` rejection path with a
   `ShellApprovalMiddleware`. It uses the parsed analysis and returns a
   private, typed `approvalRequired` signal for an approval-required Shell
   call; other tools remain a no-op.
3. Teach `Runner.RunTurn` to convert that signal into a paused `TurnOutput`
   instead of a Tool error. Preserve the user and assistant messages, all
   earlier tool results from that assistant response, events, model logs, and
   the unexecuted tool calls after the paused call. Emit `approval_required`;
   do not emit `tool_completed` for the paused call.
4. Add an explicit continuation API to the Runner. It receives persisted
   history plus the stored call and remaining calls, executes the approved call
   or synthesizes the deny/cancel Tool error, then continues remaining calls in
   order and returns to the model loop. A later approval-required Shell call
   returns another paused output.
5. Wire `cmd/agent-service/main.go` to use the new middleware and remove only
   the obsolete direct-rejection assumptions from existing tests.

**Tests:** a high-risk call does not reach the Shell tool; complete assistant
tool-call Message and prior Tool results are present in paused output; approve,
deny, and cancel resume the same conversation; later high-risk tool calls
pause again; read-only calls continue without a pause.

### 3. Extend Run Store persistence and MySQL migration

**Files:** `internal/store/store.go`, `internal/store/local/store.go`,
`internal/store/mysql/models.go`, `internal/store/mysql/store.go`,
`internal/store/storetest/contract.go`,
`migrations/mysql/002_add_pending_shell_approval.sql`, Local/MySQL store tests

1. Extend `RunStore` with load, save, compare-and-swap resolve
   (`pending` → `executing` by approval ID and version), clear, and
   executing-recovery operations. Make stale, duplicate, and unknown
   resolutions return a shared conflict error.
2. Store Local approvals in
   `data/runs/<run_id>/pending_approval.json`. Use a write-then-rename helper
   for the state file and protect same-process resolution with the store mutex
   so only one decision wins.
3. Add `pending_approval_payload LONGTEXT NULL` to `agent_run_states` in the
   new operator-applied migration. Add the GORM field and perform MySQL
   compare-and-swap in a transaction that row-locks the Run/state row.
4. Make reload inspect approvals in `executing`: clear the active approval,
   mark the Run failed/recovery-required, and persist a recovery event. It must
   not invoke the Shell tool.
5. Extend the store contract to validate persistence, reload, exactly-one
   resolution, clear behavior, and executing recovery for both stores.

**Tests:** Local and MySQL contract coverage accepts one matching decision and
rejects races/stale versions; reloaded pending approval is unchanged; recovery
from `executing` never executes a tool and produces failed metadata/event.

### 4. Persist pause state and add approval HTTP endpoints

**Files:** `internal/server/server.go`, `internal/server/server_test.go`

1. When a Runner output is paused, append its new messages and model logs,
   save the pending approval, set the Run to `awaiting_approval`, and persist
   `approval_required`. Do not emit `turn_completed`.
2. Add a read route for the current pending approval and
   `POST /v1/runs/{runID}/approvals/{approvalID}`. Use a strict JSON decoder
   (`DisallowUnknownFields`) and validate exactly the three decision strings.
3. Reject ordinary turn, stream-turn, and plan mutation/execute endpoints
   while the Run awaits approval. Return conflict for missing, stale, resolved,
   or mismatched approval IDs.
4. On resolution, atomically claim the persisted approval before the Shell
   execution. Before executing an approval, verify the stored script hash and
   re-run the risk analysis; a changed hash or newly stricter classification
   fails safely rather than running the script.
5. Append the resolved Tool message/events (`approval_approved`,
   `approval_denied`, or `approval_cancelled`), clear the executing approval
   only after that result is durable, then call the Runner continuation and
   persist its normal completion or a subsequent pause. If process failure
   occurs while `executing`, leave the marker for Step 3 recovery rather than
   retrying.

**Tests:** creating/appending a high-risk Shell request produces
`awaiting_approval` with no side effect; a GET returns the stored display data;
each decision resumes correctly; repeated/competing requests return conflict;
unknown request fields and any supplied script are rejected; normal turns are
blocked while pending; approved script executes once.

### 5. Add the console approval panel

**Files:** `internal/server/web/index.html`, `internal/server/web/app.js`,
`internal/server/web/styles.css`

1. Add an approval inspector panel shown when the selected Run is
   `awaiting_approval` or has a pending-approval response. Render the exact
   stored script in a `<pre>`, its workspace, and every risk reason.
2. Add Approve, Deny, and Cancel buttons that submit only the fixed decision
   body to the server endpoint. Do not render an editable script field.
3. Refresh approval state alongside Run metadata. After resolving, refresh
   metadata, messages, events, model logs, and history; surface API conflicts
   in the existing toast.
4. Disable the normal composer/plan actions while approval is pending and
   preserve existing busy-state behavior and keyboard accessibility.

**Tests/verification:** browser-free checks cover the API; manually verify
that a loaded awaiting Run displays the original script and each decision
unblocks the UI without allowing script editing.

### 6. Final integration and verification

**Files:** only corrections required by the preceding steps, plus `docs/adr/`
only if an implementation decision deviates from the approved design.

1. Run focused tests while each layer is changed:
   `go test ./internal/tools ./internal/agent ./internal/store/... ./internal/server`.
2. Run the complete regression suite and formatting checks:
   `gofmt -w` only on modified Go files, `go test ./...`, and `git diff --check`.
3. Manually run the service with the fake provider and validate a high-risk
   script pauses, its pending state survives a restart, and Approve/Deny/Cancel
   produce the expected persisted messages and events.
4. Confirm the migration is documented as an operator prerequisite and that
   no unrelated untracked `.DS_Store` files are staged.

## Completion checklist

- [ ] A script is approved per Shell tool call, not per command token.
- [ ] No high-risk/unknown script runs before approval.
- [ ] Approval only executes the durable, hash-verified original script.
- [ ] Deny and cancel resume with a Tool error; later calls retain order.
- [ ] Local and MySQL state survive reload and reject duplicate decisions.
- [ ] `executing` recovery is at-most-once (fail/recovery-required, no retry).
- [ ] HTTP and Web Console expose the same non-editable approval state.
- [ ] `go test ./...` and `git diff --check` pass.
