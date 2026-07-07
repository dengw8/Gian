package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/store"
)

func TestStorePersistsRunMessagesAndEvents(t *testing.T) {
	ctx := context.Background()
	localStore := New(t.TempDir())

	meta, err := localStore.CreateRun(ctx, store.CreateRunInput{ModelProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}

	messages := []runtime.Message{
		{Role: runtime.RoleUser, Content: "hello"},
		{Role: runtime.RoleAssistant, Content: "fake response: hello"},
	}
	if err := localStore.AppendMessages(ctx, meta.ID, messages); err != nil {
		t.Fatal(err)
	}

	events := []runtime.Event{
		{Type: "run_created", CreatedAt: time.Now().UTC(), Message: "Run created"},
		{Type: "turn_completed", CreatedAt: time.Now().UTC(), Message: "Turn completed"},
	}
	if err := localStore.AppendEvents(ctx, meta.ID, events); err != nil {
		t.Fatal(err)
	}

	updated, err := localStore.UpdateRun(ctx, meta.ID, store.UpdateRunInput{Status: store.RunStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != store.RunStatusCompleted {
		t.Fatalf("status = %q, want %q", updated.Status, store.RunStatusCompleted)
	}

	gotMeta, err := localStore.GetRun(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotMeta.ID != meta.ID {
		t.Fatalf("id = %q, want %q", gotMeta.ID, meta.ID)
	}

	gotMessages, err := localStore.ListMessages(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotMessages) != len(messages) {
		t.Fatalf("message count = %d, want %d", len(gotMessages), len(messages))
	}

	gotEvents, err := localStore.ListEvents(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotEvents) != len(events) {
		t.Fatalf("event count = %d, want %d", len(gotEvents), len(events))
	}
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

	for _, name := range []string{"metadata.json", "messages.jsonl", "events.jsonl"} {
		path := filepath.Join(root, "runs", meta.ID, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}

func TestStoreReturnsNotFoundForMissingRun(t *testing.T) {
	_, err := New(t.TempDir()).GetRun(context.Background(), "run_missing")
	if !errors.Is(err, store.ErrRunNotFound) {
		t.Fatalf("err = %v, want ErrRunNotFound", err)
	}
}
