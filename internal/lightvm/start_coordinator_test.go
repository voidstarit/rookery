package lightvm

import (
	"context"
	"testing"
	"time"
)

func waitFor(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

func TestStartCoordinatorSerializesSameID(t *testing.T) {
	c := NewStartCoordinatorWithLimit(2)
	firstCtx, firstDone, err := c.Begin(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	_ = firstCtx

	acquired := make(chan struct{})
	go func() {
		_, done, err := c.Begin(context.Background(), "same")
		if err != nil {
			return
		}
		close(acquired)
		done()
	}()
	select {
	case <-acquired:
		t.Fatal("second start acquired the slot before the first finished")
	case <-time.After(100 * time.Millisecond):
	}
	firstDone()
	waitFor(t, acquired, "second start did not acquire after first finished")
}

func TestStartCoordinatorDifferentIDsConcurrent(t *testing.T) {
	c := NewStartCoordinatorWithLimit(2)
	_, doneA, err := c.Begin(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer doneA()
	doneB := make(chan struct{})
	go func() {
		_, done, err := c.Begin(context.Background(), "b")
		if err == nil {
			close(doneB)
			done()
		}
	}()
	waitFor(t, doneB, "different ID did not start concurrently")
}

func TestStartCoordinatorReservationAndCancellation(t *testing.T) {
	c := NewStartCoordinatorWithLimit(2)
	releaseFirst := c.Reserve("blocked")
	releaseSecond := c.Reserve("blocked")
	started := make(chan struct{})
	go func() {
		_, done, err := c.Begin(context.Background(), "blocked")
		if err != nil {
			return
		}
		close(started)
		done()
	}()
	select {
	case <-started:
		t.Fatal("start ignored reservations")
	case <-time.After(100 * time.Millisecond):
	}
	releaseFirst()
	select {
	case <-started:
		t.Fatal("start proceeded while one reservation remained")
	case <-time.After(100 * time.Millisecond):
	}
	releaseSecond()
	waitFor(t, started, "start did not proceed after reservations released")
}

func TestStartCoordinatorCancelAbortsPreparation(t *testing.T) {
	c := NewStartCoordinatorWithLimit(2)
	startCtx, done, err := c.Begin(context.Background(), "cancel")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	c.Cancel("cancel")
	select {
	case <-startCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel did not cancel the start context")
	}
}

func TestStartCoordinatorGlobalPreparationLimit(t *testing.T) {
	c := NewStartCoordinatorWithLimit(1)
	_, doneFirst, err := c.Begin(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan struct{})
	go func() {
		_, done, err := c.Begin(context.Background(), "second")
		if err != nil {
			return
		}
		close(acquired)
		done()
	}()
	select {
	case <-acquired:
		t.Fatal("second start bypassed preparation limit")
	case <-time.After(100 * time.Millisecond):
	}
	doneFirst()
	waitFor(t, acquired, "second start did not acquire after permit release")
}

func TestStartCoordinatorCanceledWaitDoesNotLeak(t *testing.T) {
	c := NewStartCoordinatorWithLimit(1)
	_, done, err := c.Begin(context.Background(), "holder")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Begin(ctx, "waiter"); err == nil {
		t.Fatal("Begin ignored an already-canceled context")
	}
	done()
	canceledCtx, canceledDone, err := c.Begin(context.Background(), "holder")
	if err != nil {
		t.Fatal(err)
	}
	_ = canceledCtx
	canceledDone()
}
