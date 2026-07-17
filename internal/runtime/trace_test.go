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
