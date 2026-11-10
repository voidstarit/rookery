package lightvm

import (
	"context"
	"sync"
)

const defaultPreparationLimit = 2

// StartCoordinator serializes starts for one LightVM, gates cleanup against
// new starts with refcounted reservations, and bounds concurrent image
// preparation across LightVMs.
type StartCoordinator struct {
	mu      sync.Mutex
	entries map[string]*startEntry
	sem     chan struct{}
}

type startEntry struct {
	busy       bool
	wait       chan struct{}
	cancel     context.CancelFunc
	reserved   int
	reservedCh chan struct{}
}

// NewStartCoordinator creates a coordinator with the default preparation limit.
func NewStartCoordinator() *StartCoordinator {
	return NewStartCoordinatorWithLimit(defaultPreparationLimit)
}

// NewStartCoordinatorWithLimit creates a coordinator with a preparation limit.
// A non-positive limit falls back to the default.
func NewStartCoordinatorWithLimit(limit int) *StartCoordinator {
	if limit < 1 {
		limit = defaultPreparationLimit
	}
	return &StartCoordinator{
		entries: make(map[string]*startEntry),
		sem:     make(chan struct{}, limit),
	}
}

// Begin acquires the per-LightVM start slot and a global preparation permit.
// The returned context is cancelled by Cancel for this ID. Call done exactly
// once when the start, including cleanup, has finished.
func (c *StartCoordinator) Begin(ctx context.Context, id string) (context.Context, func(), error) {
	if c == nil {
		startCtx, cancel := context.WithCancel(ctx)
		return startCtx, func() { cancel() }, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	entry := c.entry(id)
	c.mu.Lock()
	for entry.busy {
		wait := entry.wait
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-wait:
		}
		c.mu.Lock()
		if entry.wait == nil {
			entry.wait = make(chan struct{})
		}
	}
	entry.busy = true
	entry.wait = make(chan struct{})
	c.mu.Unlock()

	startCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	entry.cancel = cancel
	c.mu.Unlock()

	for {
		c.mu.Lock()
		reserved := entry.reserved
		reservedCh := entry.reservedCh
		c.mu.Unlock()
		if reserved == 0 {
			break
		}
		select {
		case <-startCtx.Done():
			c.abortBegin(entry, cancel)
			return nil, nil, startCtx.Err()
		case <-reservedCh:
		}
	}

	select {
	case c.sem <- struct{}{}:
		if startCtx.Err() != nil {
			<-c.sem
			c.abortBegin(entry, cancel)
			return nil, nil, startCtx.Err()
		}
	case <-startCtx.Done():
		c.abortBegin(entry, cancel)
		return nil, nil, startCtx.Err()
	}

	var once sync.Once
	done := func() {
		once.Do(func() {
			<-c.sem
			cancel()
			c.mu.Lock()
			c.finishEntry(entry)
			c.mu.Unlock()
		})
	}
	return startCtx, done, nil
}

// Reserve increments the lifecycle reservation for id and returns an
// idempotent release. Begin waits while any reservation is held.
func (c *StartCoordinator) Reserve(id string) func() {
	if c == nil {
		return func() {}
	}
	entry := c.entry(id)
	c.mu.Lock()
	entry.reserved++
	c.signalReserved(entry)
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			if entry.reserved > 0 {
				entry.reserved--
			}
			c.signalReserved(entry)
			c.mu.Unlock()
		})
	}
}

// Cancel aborts an in-flight image preparation for id.
func (c *StartCoordinator) Cancel(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if entry := c.entries[id]; entry != nil && entry.cancel != nil {
		entry.cancel()
	}
	c.mu.Unlock()
}

func (c *StartCoordinator) entry(id string) *startEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[id]
	if entry == nil {
		entry = &startEntry{reservedCh: make(chan struct{})}
		c.entries[id] = entry
	}
	if entry.reservedCh == nil {
		entry.reservedCh = make(chan struct{})
	}
	return entry
}

func (c *StartCoordinator) abortBegin(entry *startEntry, cancel context.CancelFunc) {
	cancel()
	c.mu.Lock()
	entry.cancel = nil
	c.finishEntry(entry)
	c.mu.Unlock()
}

func (c *StartCoordinator) finishEntry(entry *startEntry) {
	entry.busy = false
	entry.cancel = nil
	if entry.wait != nil {
		close(entry.wait)
	}
	entry.wait = make(chan struct{})
}

func (c *StartCoordinator) signalReserved(entry *startEntry) {
	if entry.reservedCh != nil {
		close(entry.reservedCh)
	}
	entry.reservedCh = make(chan struct{})
}
