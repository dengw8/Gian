package trace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
)

type memoryTraceStore struct {
	mu     sync.Mutex
	spans  []runtime.TraceSpan
	events []runtime.ExecutionEvent
}

func (s *memoryTraceStore) UpsertTraceSpan(_ context.Context, span runtime.TraceSpan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, span)
	return nil
}

func (s *memoryTraceStore) ListTraceSpans(context.Context, string) ([]runtime.TraceSpan, error) {
	return nil, nil
}

func (s *memoryTraceStore) AppendExecutionEvent(_ context.Context, event runtime.ExecutionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *memoryTraceStore) ListExecutionEvents(_ context.Context, _ string, after int64, _ int) ([]runtime.ExecutionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]runtime.ExecutionEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *memoryTraceStore) LastExecutionEventSequence(context.Context, string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sequence int64
	for _, event := range s.events {
		if event.Sequence > sequence {
			sequence = event.Sequence
		}
	}
	return sequence, nil
}

func TestObserverRedactsAndPersistsAsynchronously(t *testing.T) {
	store := &memoryTraceStore{}
	observer := NewObserver(store)
	observer.Observe(context.Background(), runtime.TraceSpan{ID: "span", RunID: "run", Input: []byte(`{"token":"secret","nested":{"password":"hidden"}}`)})
	observer.Close()
	if len(store.spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(store.spans))
	}
	if string(store.spans[0].Input) != `{"nested":{"password":"[REDACTED]"},"token":"[REDACTED]"}` {
		t.Fatalf("input = %s", store.spans[0].Input)
	}
}

func TestObserverPersistsEventBeforeProjectingSpan(t *testing.T) {
	store := &memoryTraceStore{}
	observer := NewObserver(store, store)
	observer.Observe(context.Background(), runtime.TraceSpan{ID: "model", RunID: "run", Kind: runtime.TraceSpanModel, Status: runtime.TraceStatusRunning})
	observer.Close()
	if len(store.events) != 1 || store.events[0].Type != runtime.ExecutionEventModelStarted || store.events[0].Sequence != 1 {
		t.Fatalf("events = %#v", store.events)
	}
	if len(store.spans) != 1 || store.spans[0].Sequence != 1 || store.spans[0].Revision != 1 {
		t.Fatalf("spans = %#v", store.spans)
	}
}

func TestObserverContinuesSequenceAfterRestart(t *testing.T) {
	store := &memoryTraceStore{}
	first := NewObserver(store, store)
	first.Observe(context.Background(), runtime.TraceSpan{ID: "first", RunID: "run", Kind: runtime.TraceSpanRun, Status: runtime.TraceStatusRunning})
	first.Close()
	second := NewObserver(store, store)
	second.Observe(context.Background(), runtime.TraceSpan{ID: "second", RunID: "run", Kind: runtime.TraceSpanTurn, Status: runtime.TraceStatusRunning})
	second.Close()
	if len(store.events) != 2 || store.events[0].Sequence != 1 || store.events[1].Sequence != 2 {
		t.Fatalf("events = %#v, want monotonic sequences across observers", store.events)
	}
}

func TestObserverDoesNotWaitForStore(t *testing.T) {
	store := &slowTraceStore{wait: make(chan struct{})}
	observer := NewObserver(store)
	started := time.Now()
	observer.Observe(context.Background(), runtime.TraceSpan{ID: "span", RunID: "run"})
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("Observe blocked on trace store")
	}
	close(store.wait)
	observer.Close()
}

func TestObserverReportsWriteFailuresAndDroppedUpdates(t *testing.T) {
	failing := &failingTraceStore{}
	observer := NewObserver(failing)
	observer.Observe(context.Background(), runtime.TraceSpan{ID: "span", RunID: "run"})
	observer.Close()
	diagnostics := observer.Diagnostics()
	if diagnostics.Complete || diagnostics.LastError == "" {
		t.Fatalf("diagnostics = %#v, want write failure", diagnostics)
	}

	slow := &slowTraceStore{wait: make(chan struct{})}
	observer = NewObserver(slow)
	for i := 0; i < defaultBufferSize+32; i++ {
		observer.Observe(context.Background(), runtime.TraceSpan{ID: "span", RunID: "run"})
	}
	if diagnostics = observer.Diagnostics(); diagnostics.DroppedUpdates == 0 || diagnostics.Complete {
		t.Fatalf("diagnostics = %#v, want dropped updates", diagnostics)
	}
	close(slow.wait)
	observer.Close()
}

func TestObserverTruncatesOversizedModelText(t *testing.T) {
	store := &memoryTraceStore{}
	observer := NewObserver(store)
	observer.PublishExecutionEvent(context.Background(), runtime.ExecutionEvent{RunID: "run", Type: runtime.ExecutionEventModelText, Payload: []byte(`{"delta":"` + strings.Repeat("x", maxModelTextPayloadBytes) + `"}`)})
	observer.Close()
	if diagnostics := observer.Diagnostics(); diagnostics.TruncatedEvents != 1 || diagnostics.Complete {
		t.Fatalf("diagnostics = %#v, want one truncated event", diagnostics)
	}
}

type slowTraceStore struct{ wait chan struct{} }

type failingTraceStore struct{}

func (failingTraceStore) UpsertTraceSpan(context.Context, runtime.TraceSpan) error {
	return errors.New("store unavailable")
}
func (failingTraceStore) ListTraceSpans(context.Context, string) ([]runtime.TraceSpan, error) {
	return nil, nil
}

func (s *slowTraceStore) UpsertTraceSpan(context.Context, runtime.TraceSpan) error {
	<-s.wait
	return nil
}
func (s *slowTraceStore) ListTraceSpans(context.Context, string) ([]runtime.TraceSpan, error) {
	return nil, nil
}
