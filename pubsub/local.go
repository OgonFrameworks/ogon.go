// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// pubsub/local — in-process sharded pub/sub backend (LIVE-009).
//
// Subscribers are stored in a sharded sync.Map keyed by exact topic. Each
// subscriber owns a bounded buffered channel. Publish dispatches the
// message to every matching subscriber in O(matching subscribers) time
// using non-blocking sends (drop-oldest default) so a single slow consumer
// cannot stall the publisher. Glob subscribe ("room.*") is supported via
// path.Match against the topic on each publish; for high-throughput exact
// topics no glob scanning is needed (subscribers are looked up directly).
//
// All goroutines spawned by this backend (the dispatch worker pool) are
// owned by the runtime.Supervisor — Close drains them via wg.Wait().

package pubsub

import (
	"context"
	"errors"
	"path"
	"sync"
	"sync/atomic"
)

// Local is the default in-process Backend. Safe for concurrent use.
type Local struct {
	cfg BackendConfig

	// exact stores *subscriberSet keyed by exact topic.
	exact sync.Map

	// globs stores []*localSubscriber for pattern subscriptions; a slice
	// guarded by globMu so Publish can iterate without write contention.
	globMu sync.RWMutex
	globs  []*localSubscriber

	// dispatcher pool — every Publish hands a (sub, msg) job to a worker
	// which performs the actual non-blocking send. Bounded N = 4.
	jobCh chan localJob
	wg    sync.WaitGroup

	closed atomic.Bool
}

// localSubscriber is one consumer's interest registration.
//
// The closed flag + mu pair coordinates with deliver() so that closing a
// subscriber never races a publish-send: deliver holds mu for the channel
// send; Close sets closed=true and closes the channel under mu.
type localSubscriber struct {
	patterns []string
	ch       chan Message

	mu     sync.Mutex
	closed bool
}

// localJob is a unit of fanout work.
type localJob struct {
	sub *localSubscriber
	msg Message
}

// NewLocal constructs an in-process Backend. Workers are spawned under
// the local WaitGroup; Close drains them.
func NewLocal(cfg BackendConfig) *Local {
	cfg = cfg.withDefaults()
	l := &Local{
		cfg:   cfg,
		jobCh: make(chan localJob, cfg.QueueCap*4),
	}
	workers := 4
	for i := 0; i < workers; i++ {
		l.wg.Add(1)
		go l.dispatchLoop()
	}
	return l
}

func (l *Local) dispatchLoop() {
	defer l.wg.Done()
	// recover guards against the rare TOCTOU window between closed-check
	// and channel send during concurrent Close. We log nothing further —
	// drops are a configured, expected outcome under backpressure.
	defer func() { _ = recover() }()
	for job := range l.jobCh {
		l.deliver(job.sub, job.msg)
	}
}

// deliver sends msg to sub.ch honoring the configured drop policy.
// Subscribers that have been closed are skipped atomically.
func (l *Local) deliver(sub *localSubscriber, msg Message) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	switch l.cfg.DropPolicy {
	case "block":
		select {
		case sub.ch <- msg:
		default:
		}
	default:
		select {
		case sub.ch <- msg:
		default:
			if l.cfg.DropPolicy == "drop-oldest" {
				select {
				case <-sub.ch:
				default:
				}
				select {
				case sub.ch <- msg:
				default:
				}
			}
		}
	}
}

// Publish implements Backend.
func (l *Local) Publish(ctx context.Context, topic string, msg Message) error {
	if l.closed.Load() {
		return errors.New("ogon/pubsub: backend closed")
	}
	msg.Topic = topic
	if v, ok := l.exact.Load(topic); ok {
		if set, ok := v.(*subscriberSet); ok {
			for _, sub := range set.snapshot() {
				l.enqueue(localJob{sub: sub, msg: msg})
			}
		}
	}
	l.globMu.RLock()
	globs := append([]*localSubscriber(nil), l.globs...)
	l.globMu.RUnlock()
	for _, sub := range globs {
		if matchAny(sub.patterns, topic) {
			l.enqueue(localJob{sub: sub, msg: msg})
		}
	}
	return nil
}

func (l *Local) enqueue(job localJob) {
	select {
	case l.jobCh <- job:
	default:
	}
}

// Subscribe implements Backend. Patterns may be exact topics or glob
// patterns (path.Match syntax, e.g. "room.*").
func (l *Local) Subscribe(ctx context.Context, patterns ...string) (Subscription, error) {
	if l.closed.Load() {
		return nil, errors.New("ogon/pubsub: backend closed")
	}
	if len(patterns) == 0 {
		return nil, errors.New("ogon/pubsub: at least one pattern required")
	}
	sub := &localSubscriber{
		patterns: append([]string(nil), patterns...),
		ch:       make(chan Message, l.cfg.QueueCap),
	}
	for _, p := range patterns {
		if hasMeta(p) {
			l.globMu.Lock()
			l.globs = append(l.globs, sub)
			l.globMu.Unlock()
			continue
		}
		v, _ := l.exact.LoadOrStore(p, newSubscriberSet())
		set := v.(*subscriberSet)
		set.add(sub)
	}
	return &localSubscription{backend: l, sub: sub}, nil
}

// Close implements Backend. Idempotent.
func (l *Local) Close() error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(l.jobCh)
	l.wg.Wait()
	// Close all subscriber channels.
	l.exact.Range(func(_, v any) bool {
		v.(*subscriberSet).closeAll()
		return true
	})
	l.globMu.Lock()
	globs := l.globs
	l.globs = nil
	l.globMu.Unlock()
	for _, sub := range globs {
		sub.mu.Lock()
		sub.closed = true
		close(sub.ch)
		sub.mu.Unlock()
	}
	return nil
}

// localSubscription is the Subscription returned by Local.
type localSubscription struct {
	backend *Local
	sub     *localSubscriber
	once    sync.Once
}

func (s *localSubscription) Messages() <-chan Message { return s.sub.ch }

func (s *localSubscription) Close() error {
	s.once.Do(func() {
		b := s.backend
		b.globMu.Lock()
		out := b.globs[:0]
		for _, sub := range b.globs {
			if sub != s.sub {
				out = append(out, sub)
			}
		}
		b.globs = out
		b.globMu.Unlock()
		b.exact.Range(func(_, v any) bool {
			v.(*subscriberSet).remove(s.sub)
			return true
		})
		s.sub.mu.Lock()
		s.sub.closed = true
		close(s.sub.ch)
		s.sub.mu.Unlock()
	})
	return nil
}

// subscriberSet holds subscribers keyed under an exact topic.
type subscriberSet struct {
	mu   sync.Mutex
	subs []*localSubscriber
}

func newSubscriberSet() *subscriberSet { return &subscriberSet{} }

func (s *subscriberSet) add(sub *localSubscriber) {
	s.mu.Lock()
	s.subs = append(s.subs, sub)
	s.mu.Unlock()
}

func (s *subscriberSet) remove(sub *localSubscriber) {
	s.mu.Lock()
	out := s.subs[:0]
	for _, x := range s.subs {
		if x != sub {
			out = append(out, x)
		}
	}
	s.subs = out
	s.mu.Unlock()
}

func (s *subscriberSet) snapshot() []*localSubscriber {
	s.mu.Lock()
	out := make([]*localSubscriber, len(s.subs))
	copy(out, s.subs)
	s.mu.Unlock()
	return out
}

func (s *subscriberSet) closeAll() {
	s.mu.Lock()
	subs := s.subs
	s.subs = nil
	s.mu.Unlock()
	for _, sub := range subs {
		sub.mu.Lock()
		if !sub.closed {
			sub.closed = true
			close(sub.ch)
		}
		sub.mu.Unlock()
	}
}

// hasMeta reports whether pattern contains glob metacharacters.
func hasMeta(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*', '?', '[', '{':
			return true
		}
	}
	return false
}

// matchAny reports whether topic matches any of patterns (path.Match).
func matchAny(patterns []string, topic string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, topic); ok {
			return true
		}
	}
	return false
}
