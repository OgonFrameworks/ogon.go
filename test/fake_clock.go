// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fake clock injection (TEST-007). Time-dependent code is hard to test;
// this fixture gives every caller a single, controllable clock they can
// advance deterministically. Tests inject the clock by passing it as the
// time source to the production code path; production code accepts a
// Clock interface rather than calling time.Now directly.

package test

import (
	"sync"
	"sync/atomic"
	"time"
)

// Clock is the interface used by production code. time.Now becomes Now().
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// FakeClock implements Clock for tests. Now is monotonic with respect to
// Advance. After fires when the clock passes the requested deadline.
type FakeClock struct {
	mu      sync.Mutex
	current int64 // unix-nanos
	waiters []fakeWaiter
}

type fakeWaiter struct {
	deadline int64
	ch       chan time.Time
}

// NewFakeClock constructs a clock pinned to the supplied unix-nanosecond
// instant. Tests typically pin to a known value and Advance from there.
func NewFakeClock(now int64) *FakeClock {
	return &FakeClock{current: now}
}

// Now returns the clock's current time.
func (c *FakeClock) Now() time.Time {
	cur := atomic.LoadInt64(&c.current)
	return time.Unix(0, cur)
}

// Advance moves the clock forward by d. Any After waiters whose deadline
// has passed are fired in registration order.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	atomic.StoreInt64(&c.current, c.current+int64(d))
	cur := atomic.LoadInt64(&c.current)
	// Fire all eligible waiters.
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if cur >= w.deadline {
			close(w.ch)
			continue
		}
		kept = append(kept, w)
	}
	c.waiters = kept
}

// After registers a waiter that fires when the clock advances past d.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := atomic.LoadInt64(&c.current)
	c.waiters = append(c.waiters, fakeWaiter{deadline: cur + int64(d), ch: ch})
	return ch
}

// SetAbsolute jumps the clock to the supplied unix-nanosecond instant.
// Useful for tests that need to simulate "tomorrow at noon".
func (c *FakeClock) SetAbsolute(unixNano int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	atomic.StoreInt64(&c.current, unixNano)
	cur := unixNano
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if cur >= w.deadline {
			close(w.ch)
			continue
		}
		kept = append(kept, w)
	}
	c.waiters = kept
}

// CurrentNanos returns the current clock value without constructing a
// time.Time (hot path for some benchmark setups).
func (c *FakeClock) CurrentNanos() int64 { return atomic.LoadInt64(&c.current) }

// assert Clock implementation at compile time
var _ Clock = (*FakeClock)(nil)

// RealClock is the production Clock backed by the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var _ Clock = RealClock{}
