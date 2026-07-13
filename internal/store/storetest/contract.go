package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/store"
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

		updated, err = runStore.UpdateRun(context.Background(), meta.ID, store.UpdateRunInput{
			Status: store.RunStatusCompleted,
			AddUsage: &runtime.TokenUsage{
				PromptTokens:     intPtr(10),
				CompletionTokens: intPtr(3),
				TotalTokens:      intPtr(13),
				CachedTokens:     intPtr(4),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		updated, err = runStore.UpdateRun(context.Background(), meta.ID, store.UpdateRunInput{
			Status: store.RunStatusCompleted,
			AddUsage: &runtime.TokenUsage{
				PromptTokens: intPtr(2),
				TotalTokens:  intPtr(2),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if updated.TokenUsage == nil || *updated.TokenUsage.PromptTokens != 12 || *updated.TokenUsage.TotalTokens != 15 || *updated.TokenUsage.CachedTokens != 4 {
			t.Fatalf("token usage = %#v", updated.TokenUsage)
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

		logs := []runtime.ModelLog{{
			Index:       1,
			StartedAt:   messageTime,
			CompletedAt: messageTime.Add(time.Millisecond),
			Request: runtime.ModelRequestLog{
				SystemPrompt: "system",
				Messages:     messages[:1],
			},
			Response: runtime.ModelResponseLog{Message: messages[1]},
		}}
		if err := runStore.AppendModelLogs(context.Background(), meta.ID, logs); err != nil {
			t.Fatal(err)
		}
		gotLogs, err := runStore.ListModelLogs(context.Background(), meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotLogs, logs) {
			t.Fatalf("model logs = %#v, want %#v", gotLogs, logs)
		}
	})

	t.Run("plan state", func(t *testing.T) {
		runStore := newStore(t)
		meta, err := runStore.CreateRun(context.Background(), store.CreateRunInput{ModelProvider: "fake"})
		if err != nil {
			t.Fatal(err)
		}

		empty, err := runStore.GetPlan(context.Background(), meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if empty.Mode != runtime.PlanModeOff {
			t.Fatalf("empty plan mode = %q, want off", empty.Mode)
		}

		plan := runtime.PlanState{
			Mode: runtime.PlanModePlanning,
			Steps: []runtime.PlanStep{
				{Index: 1, Text: "Inspect current behavior", Status: runtime.PlanStepCompleted},
				{Index: 2, Text: "Implement the change", Status: runtime.PlanStepPending},
			},
		}
		updated, err := runStore.SavePlan(context.Background(), meta.ID, plan)
		if err != nil {
			t.Fatal(err)
		}
		if updated.PlanMode != string(runtime.PlanModePlanning) {
			t.Fatalf("metadata plan mode = %q", updated.PlanMode)
		}
		if updated.PlanSteps != 2 || updated.PlanCompleted != 1 {
			t.Fatalf("metadata plan summary = steps %d completed %d", updated.PlanSteps, updated.PlanCompleted)
		}

		got, err := runStore.GetPlan(context.Background(), meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mode != runtime.PlanModePlanning || len(got.Steps) != 2 {
			t.Fatalf("plan = %#v", got)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Fatalf("plan timestamps not set: %#v", got)
		}
	})

	t.Run("compaction state", func(t *testing.T) {
		runStore := newStore(t)
		meta, err := runStore.CreateRun(context.Background(), store.CreateRunInput{ModelProvider: "fake"})
		if err != nil {
			t.Fatal(err)
		}

		empty, err := runStore.GetCompaction(context.Background(), meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if empty.Enabled {
			t.Fatalf("empty compaction = %#v, want disabled", empty)
		}

		state := runtime.CompactionState{
			Enabled:           true,
			Summary:           "summary",
			CompactedMessages: 4,
			KeepRecent:        2,
			UpdatedAt:         time.Now().UTC(),
		}
		if err := runStore.SaveCompaction(context.Background(), meta.ID, state); err != nil {
			t.Fatal(err)
		}
		got, err := runStore.GetCompaction(context.Background(), meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, state) {
			t.Fatalf("compaction = %#v, want %#v", got, state)
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

func intPtr(value int) *int {
	return &value
}
