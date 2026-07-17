package trace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
)

type memoryTraceStore struct {
	mu    sync.Mutex
	spans []runtime.TraceSpan
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
