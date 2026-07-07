# Run and Turn HTTP API

The Agent Service will expose durable Runs and append-only Turns rather than treating each request as an isolated execution. This keeps multi-turn conversation state explicit: clients create or inspect a Run, then submit Turns through non-streaming or streaming endpoints while messages and events are persisted under the Run.
