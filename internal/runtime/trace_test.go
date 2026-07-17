package runtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTraceSpanOmitsZeroCompletionTime(t *testing.T) {
	payload, err := json.Marshal(TraceSpan{ID: "span", RunID: "run", StartedAt: time.Now().UTC(), Status: TraceStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "completed_at") {
		t.Fatalf("zero completion time must be omitted, got %s", payload)
	}
}

func TestExecutionEventJSONCarriesStableCorrelationFields(t *testing.T) {
	payload, err := json.Marshal(ExecutionEvent{
		SchemaVersion: ExecutionEventSchemaVersion,
		ID:            "event_1",
		RunID:         "run_1",
		TurnID:        "turn_1",
		Iteration:     2,
		Sequence:      7,
		OccurredAt:    time.Now().UTC(),
		Type:          ExecutionEventModelCompleted,
		SpanID:        "trace_model_call_1",
		ModelCallID:   "call_1",
		Payload:       json.RawMessage(`{"response":"ok"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema_version":1`, `"sequence":7`, `"iteration":2`, `"model_call_id":"call_1"`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("payload %s does not contain %s", payload, want)
		}
	}
}
