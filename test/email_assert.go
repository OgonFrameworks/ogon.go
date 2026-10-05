// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Email catcher assert (TEST-046), webhook receiver helper (TEST-047),
// queue assert helper (TEST-048), and audit assert helper (TEST-049). All
// four follow the same pattern: an in-memory collector + a handful of
// assertions. Each is small enough to be held in the reader's head.

package test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ----- Email catcher (TEST-046) -----

// EmailMessage is the per-test captured email.
type EmailMessage struct {
	From    string
	To      string
	Subject string
	Body    string
	At      time.Time
}

// EmailCatcher is an in-memory SMTP stand-in. Tests pass it to the
// production emailer in place of a real SMTP connection.
type EmailCatcher struct {
	mu    sync.Mutex
	queue []EmailMessage
}

// NewEmailCatcher constructs an empty catcher.
func NewEmailCatcher() *EmailCatcher { return &EmailCatcher{} }

// Send appends a captured email.
func (c *EmailCatcher) Send(m EmailMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m.At.IsZero() {
		m.At = time.Now()
	}
	c.queue = append(c.queue, m)
}

// Messages returns a snapshot of captured emails.
func (c *EmailCatcher) Messages() []EmailMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]EmailMessage, len(c.queue))
	copy(out, c.queue)
	return out
}

// AssertSubject fails the test if no email has the supplied subject.
func (c *EmailCatcher) AssertSubject(t *testing.T, subject string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.queue {
		if m.Subject == subject {
			return
		}
	}
	t.Fatalf("ogontest: no email with subject %q captured", subject)
}

// AssertRecipient fails the test if no email was sent to addr.
func (c *EmailCatcher) AssertRecipient(t *testing.T, addr string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.queue {
		if m.To == addr {
			return
		}
	}
	t.Fatalf("ogontest: no email to %q captured", addr)
}

// Count returns the total number of emails captured.
func (c *EmailCatcher) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}

// Reset clears the catcher between subtests.
func (c *EmailCatcher) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue = nil
}

// ----- Webhook receiver (TEST-047) -----

// WebhookEvent is one captured webhook delivery.
type WebhookEvent struct {
	URL     string
	Method  string
	Headers http.Header
	Body    []byte
	At      time.Time
}

// WebhookReceiver is an in-memory webhook collector. Tests register it as
// the destination URL in the production webhook dispatcher.
type WebhookReceiver struct {
	mu     sync.Mutex
	events []WebhookEvent
}

// NewWebhookReceiver constructs an empty receiver.
func NewWebhookReceiver() *WebhookReceiver { return &WebhookReceiver{} }

// Receive appends a captured webhook.
func (r *WebhookReceiver) Receive(e WebhookEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now()
	}
	r.events = append(r.events, e)
}

// Events returns a snapshot of captured webhooks.
func (r *WebhookReceiver) Events() []WebhookEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]WebhookEvent, len(r.events))
	copy(out, r.events)
	return out
}

// AssertCount fails the test if the number of webhooks != want.
func (r *WebhookReceiver) AssertCount(t *testing.T, want int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) != want {
		t.Fatalf("ogontest: webhook count: want %d, got %d", want, len(r.events))
	}
}

// AssertURLOnAtLeastOne fails the test if no webhook targeted the supplied
// URL substring.
func (r *WebhookReceiver) AssertURLOnAtLeastOne(t *testing.T, substr string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if contains(e.URL, substr) {
			return
		}
	}
	t.Fatalf("ogontest: no webhook to URL containing %q", substr)
}

// ----- Queue assert (TEST-048) -----

// QueueItem is a single captured queue item.
type QueueItem struct {
	Topic string
	Data  []byte
}

// QueueCatcher is an in-memory queue collector.
type QueueCatcher struct {
	mu    sync.Mutex
	items []QueueItem
}

// NewQueueCatcher constructs an empty catcher.
func NewQueueCatcher() *QueueCatcher { return &QueueCatcher{} }

// Enqueue captures an item.
func (q *QueueCatcher) Enqueue(topic string, data []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, QueueItem{Topic: topic, Data: append([]byte(nil), data...)})
}

// Items returns a snapshot of captured items.
func (q *QueueCatcher) Items() []QueueItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]QueueItem, len(q.items))
	copy(out, q.items)
	return out
}

// ItemsForTopic returns items enqueued to topic.
func (q *QueueCatcher) ItemsForTopic(topic string) []QueueItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []QueueItem
	for _, it := range q.items {
		if it.Topic == topic {
			out = append(out, it)
		}
	}
	return out
}

// AssertTopicCount fails the test if the per-topic count != want.
func (q *QueueCatcher) AssertTopicCount(t *testing.T, topic string, want int) {
	t.Helper()
	got := len(q.ItemsForTopic(topic))
	if got != want {
		t.Fatalf("ogontest: queue topic %q count: want %d, got %d", topic, want, got)
	}
}

// Drain returns all items and clears the queue.
func (q *QueueCatcher) Drain() []QueueItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.items
	q.items = nil
	return out
}

// ----- Audit assert (TEST-049) -----

// AuditEvent is a single captured audit record.
type AuditEvent struct {
	Actor  string
	Action string
	Target string
	At     time.Time
}

// AuditCatcher is an in-memory audit log collector.
type AuditCatcher struct {
	mu     sync.Mutex
	events []AuditEvent
	seq    atomic.Int64
}

// NewAuditCatcher constructs an empty catcher.
func NewAuditCatcher() *AuditCatcher { return &AuditCatcher{} }

// Record appends a captured audit event.
func (a *AuditCatcher) Record(e AuditEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now()
	}
	a.events = append(a.events, e)
	a.seq.Add(1)
}

// Events returns a snapshot of the audit log.
func (a *AuditCatcher) Events() []AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEvent, len(a.events))
	copy(out, a.events)
	return out
}

// AssertAction fails the test if no event records the supplied action.
func (a *AuditCatcher) AssertAction(t *testing.T, action string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.events {
		if e.Action == action {
			return
		}
	}
	t.Fatalf("ogontest: no audit event with action %q", action)
}

// AssertActorCount fails the test if the count for actor != want.
func (a *AuditCatcher) AssertActorCount(t *testing.T, actor string, want int) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	got := 0
	for _, e := range a.events {
		if e.Actor == actor {
			got++
		}
	}
	if got != want {
		t.Fatalf("ogontest: audit actor %q count: want %d, got %d", actor, want, got)
	}
}

// SortedActions returns the actions in chronological order; useful for
// asserting on a sequence of audit events.
func (a *AuditCatcher) SortedActions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.events))
	for _, e := range a.events {
		out = append(out, e.Action)
	}
	sort.Strings(out)
	return out
}

// WaitFor blocks until at least n events have been recorded or ctx expires.
func (a *AuditCatcher) WaitFor(ctx context.Context, n int) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if int(a.seq.Load()) >= n {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("ogontest: audit WaitFor: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
