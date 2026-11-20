package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerRejectsDuplicateAndRunsSerially(t *testing.T) {
	var concurrent int32
	var maximum int32
	m := New(context.Background(), 5*time.Millisecond, func(ctx context.Context, _ string) error {
		current := atomic.AddInt32(&concurrent, 1)
		for {
			seen := atomic.LoadInt32(&maximum)
			if current <= seen || atomic.CompareAndSwapInt32(&maximum, seen, current) {
				break
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(12 * time.Millisecond):
		}
		atomic.AddInt32(&concurrent, -1)
		return nil
	})
	if err := m.Start("one"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("one"); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	time.Sleep(35 * time.Millisecond)
	m.Stop("one")
	if maximum != 1 {
		t.Fatalf("expected serial runs, maximum concurrency was %d", maximum)
	}
	if m.RunningCount() != 0 {
		t.Fatal("job should be removed after stop")
	}
}

func TestStopCancelsAndWaitsForRunner(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	m := New(context.Background(), time.Hour, func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	})
	if err := m.Start("one"); err != nil {
		t.Fatal(err)
	}
	<-started
	m.Stop("one")
	select {
	case <-finished:
	default:
		t.Fatal("stop returned before runner finished")
	}
}
