package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/store"
	"github.com/dengw8/Gian/internal/store/storetest"
)

func TestRunStoreContract(t *testing.T) {
	storetest.RunStoreContract(t, func(t *testing.T) store.RunStore {
		t.Helper()
		return New(t.TempDir())
	})
}

func TestStoreUsesInspectableRunLayout(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	localStore := New(root)

	meta, err := localStore.CreateRun(ctx, store.CreateRunInput{ModelProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := localStore.AppendMessages(ctx, meta.ID, []runtime.Message{{Role: runtime.RoleUser, Content: "hello"}}); err != nil {
		t.Fatal(err)
	}
	if err := localStore.AppendEvents(ctx, meta.ID, []runtime.Event{{Type: "run_created", CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	if err := localStore.AppendModelLogs(ctx, meta.ID, []runtime.ModelLog{{
		Index:     1,
		StartedAt: time.Now().UTC(),
		Request:   runtime.ModelRequestLog{SystemPrompt: "system"},
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := localStore.SavePlan(ctx, meta.ID, runtime.PlanState{Mode: runtime.PlanModePlanning}); err != nil {
		t.Fatal(err)
	}
	if err := localStore.SaveCompaction(ctx, meta.ID, runtime.CompactionState{Enabled: true, Summary: "summary"}); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"metadata.json", "messages.jsonl", "events.jsonl", "model_logs.jsonl", "plan.json", "compaction.json"} {
		path := filepath.Join(root, "runs", meta.ID, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}

func TestTraceSpansKeepLatestRevision(t *testing.T) {
	ctx := context.Background()
	localStore := New(t.TempDir())
	meta, err := localStore.CreateRun(ctx, store.CreateRunInput{ModelProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	if err := localStore.UpsertTraceSpan(ctx, runtime.TraceSpan{ID: "span_1", RunID: meta.ID, Kind: runtime.TraceSpanModel, Name: "Model", Status: runtime.TraceStatusSucceeded, Revision: 2, Sequence: 2, StartedAt: started}); err != nil {
		t.Fatal(err)
	}
	if err := localStore.UpsertTraceSpan(ctx, runtime.TraceSpan{ID: "span_1", RunID: meta.ID, Kind: runtime.TraceSpanModel, Name: "Model", Status: runtime.TraceStatusRunning, Revision: 1, Sequence: 2, StartedAt: started}); err != nil {
		t.Fatal(err)
	}
	spans, err := localStore.ListTraceSpans(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || spans[0].Status != runtime.TraceStatusSucceeded {
		t.Fatalf("spans = %#v, want latest succeeded span", spans)
	}
}

func TestExecutionEventsAreOrderedIncrementallyAndIdempotently(t *testing.T) {
	ctx := context.Background()
	localStore := New(t.TempDir())
	meta, err := localStore.CreateRun(ctx, store.CreateRunInput{ModelProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []runtime.ExecutionEvent{
		{SchemaVersion: 1, ID: "event_2", RunID: meta.ID, Sequence: 2, OccurredAt: time.Now().UTC(), Type: runtime.ExecutionEventModelCompleted},
		{SchemaVersion: 1, ID: "event_1", RunID: meta.ID, Sequence: 1, OccurredAt: time.Now().UTC(), Type: runtime.ExecutionEventModelStarted},
		{SchemaVersion: 1, ID: "event_1_retry", RunID: meta.ID, Sequence: 1, OccurredAt: time.Now().UTC(), Type: runtime.ExecutionEventModelStarted},
	} {
		if err := localStore.AppendExecutionEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := localStore.ListExecutionEvents(ctx, meta.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Sequence != 2 {
		t.Fatalf("events = %#v, want only sequence 2", events)
	}
	sequence, err := localStore.LastExecutionEventSequence(ctx, meta.ID)
	if err != nil || sequence != 2 {
		t.Fatalf("last sequence = %d, err=%v, want 2", sequence, err)
	}
}

func TestStoreReadsLargeMessageLines(t *testing.T) {
	ctx := context.Background()
	localStore := New(t.TempDir())

	meta, err := localStore.CreateRun(ctx, store.CreateRunInput{ModelProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	largeData := strings.Repeat("a", 128*1024)
	message := runtime.Message{
		Role:    runtime.RoleUser,
		Content: "large image",
		Attachments: []runtime.Attachment{{
			Type:      "image",
			MediaType: "image/png",
			Data:      largeData,
			Name:      "large.png",
		}},
	}
	if err := localStore.AppendMessages(ctx, meta.ID, []runtime.Message{message}); err != nil {
		t.Fatal(err)
	}

	got, err := localStore.ListMessages(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("message count = %d, want 1", len(got))
	}
	if got[0].Attachments[0].Data != largeData {
		t.Fatalf("large attachment was not preserved")
	}
}
