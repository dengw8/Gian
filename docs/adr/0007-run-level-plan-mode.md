# Run-Level Plan Mode

Plan Mode will be a persisted Run-level state rather than a global service switch or a frontend-only toggle.

This keeps planning resumable across browser refreshes, lets API clients inspect and control the workflow, and keeps the agent runtime deterministic in tests. A Run can enter planning mode, produce a numbered plan through read-only exploration, wait for explicit execution, and then track completion while executing the plan.

The first implementation stores Plan State through the Run Store boundary. The Local Store writes it under the Run directory, while future stores can persist the same state in a database without changing the HTTP or runtime behavior.

Plan Mode deliberately blocks destructive tool use while planning. Execution mode restores the normal tool set and uses persisted Plan Steps plus completion markers to update progress.
