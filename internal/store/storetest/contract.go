package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/store"
)

type Factory func(t *testing.T) store.RunStore

func RunStoreContract(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("empty list", func(t *testing.T) {
		runStore := newStore(t)

		runs, err := runStore.ListRuns(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 0 {
			t.Fatalf("run count = %d, want 0", len(runs))
		}
	})

	t.Run("missing run", func(t *testing.T) {
		runStore := newStore(t)

		_, err := runStore.GetRun(context.Background(), "run_missing")
		if !errors.Is(err, store.ErrRunNotFound) {
			t.Fatalf("err = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("metadata lifecycle", func(t *testing.T) {
		runStore := newStore(t)

		meta, err := runStore.CreateRun(context.Background(), store.CreateRunInput{ModelProvider: "fake"})
		if err != nil {
			t.Fatal(err)
		}
		if meta.ID == "" {
			t.Fatal("expected run id")
		}
		if meta.Status != store.RunStatusCreated {
			t.Fatalf("status = %q, want created", meta.Status)
		}
		if meta.ModelProvider != "fake" {
			t.Fatalf("model provider = %q, want fake", meta.ModelProvider)
		}
		if meta.CreatedAt.IsZero() || meta.UpdatedAt.IsZero() {
			t.Fatalf("expected non-zero timestamps: %#v", meta)
		}

		got, err := runStore.GetRun(context.Background(), meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, meta) {
			t.Fatalf("metadata = %#v, want %#v", got, meta)
		}

		updated, err := runStore.UpdateRun(context.Background(), meta.ID, store.UpdateRunInput{
			Status: store.RunStatusFailed,
			Error:  "model failed",
		})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Status != store.RunStatusFailed {
			t.Fatalf("status = %q, want failed", updated.Status)
		}
		if updated.Error != "model failed" {
			t.Fatalf("error = %q, want model failed", updated.Error)
		}
		if updated.UpdatedAt.Before(meta.UpdatedAt) {
			t.Fatalf("updated_at moved backwards: before=%s after=%s", meta.UpdatedAt, updated.UpdatedAt)
		}
	})

	t.Run("messages and events", func(t *testing.T) {
		runStore := newStore(t)
		meta, err := runStore.CreateRun(context.Background(), store.CreateRunInput{ModelProvider: "fake"})
		if err != nil {
			t.Fatal(err)
		}

		messageTime := time.Now().UTC()
		messages := []runtime.Message{
			{Role: runtime.RoleUser, Content: "hello", CreatedAt: messageTime},
			{Role: runtime.RoleAssistant, Content: "fake response: hello", CreatedAt: messageTime.Add(time.Millisecond)},
		}
		if err := runStore.AppendMessages(context.Background(), meta.ID, messages); err != nil {
			t.Fatal(err)
		}

		gotMessages, err := runStore.ListMessages(context.Background(), meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotMessages, messages) {
			t.Fatalf("messages = %#v, want %#v", gotMessages, messages)
		}

		events := []runtime.Event{
			{Type: "run_created", CreatedAt: messageTime, Message: "Run created"},
			{Type: "turn_completed", CreatedAt: messageTime.Add(time.Millisecond), Message: "Turn completed"},
		}
		if err := runStore.AppendEvents(context.Background(), meta.ID, events); err != nil {
			t.Fatal(err)
		}

		gotEvents, err := runStore.ListEvents(context.Background(), meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotEvents, events) {
			t.Fatalf("events = %#v, want %#v", gotEvents, events)
		}
	})

	t.Run("list runs newest updated first", func(t *testing.T) {
		runStore := newStore(t)

		first, err := runStore.CreateRun(context.Background(), store.CreateRunInput{ModelProvider: "fake"})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
		second, err := runStore.CreateRun(context.Background(), store.CreateRunInput{ModelProvider: "fake"})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
		if _, err := runStore.UpdateRun(context.Background(), first.ID, store.UpdateRunInput{Status: store.RunStatusCompleted}); err != nil {
			t.Fatal(err)
		}

		runs, err := runStore.ListRuns(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 2 {
			t.Fatalf("run count = %d, want 2", len(runs))
		}
		if runs[0].ID != first.ID || runs[1].ID != second.ID {
			t.Fatalf("runs order = %#v, want updated run first", runs)
		}
	})
}
