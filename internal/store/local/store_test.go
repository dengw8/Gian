package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/store"
	"github.com/dengw8/harness-practice/internal/store/storetest"
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

	for _, name := range []string{"metadata.json", "messages.jsonl", "events.jsonl", "model_logs.jsonl", "plan.json"} {
		path := filepath.Join(root, "runs", meta.ID, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
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
