// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryDLQPutListReplay(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	dlq := NewInMemoryDLQ(q, DefaultRetryPolicy)
	env := Envelope{ID: "x1", Name: "test", Payload: []byte("{}")}
	if err := dlq.Put(context.Background(), DLQEntry{Envelope: env, Reason: "fatal", QuarantinedAt: time.Now()}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if n, _ := dlq.Len(context.Background()); n != 1 {
		t.Fatalf("dlq len = %d, want 1", n)
	}
	entries, _ := dlq.List(context.Background(), 10)
	if len(entries) != 1 || entries[0].Envelope.ID != "x1" {
		t.Fatalf("dlq list wrong: %+v", entries)
	}
	n, err := dlq.Replay(context.Background(), []string{"x1"})
	if err != nil || n != 1 {
		t.Fatalf("replay: n=%d err=%v", n, err)
	}
	if n, _ := dlq.Len(context.Background()); n != 0 {
		t.Errorf("dlq len after replay = %d, want 0", n)
	}
}

func TestInMemoryDLQPurgeAll(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	dlq := NewInMemoryDLQ(q, DefaultRetryPolicy)
	for i := 0; i < 5; i++ {
		_ = dlq.Put(context.Background(), DLQEntry{Envelope: Envelope{ID: NewID(), Name: "t"}, Reason: "fatal"})
	}
	n, _ := dlq.Purge(context.Background(), nil)
	if n != 5 {
		t.Fatalf("purge all = %d, want 5", n)
	}
}

func TestInMemoryDLQPurgeById(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	dlq := NewInMemoryDLQ(q, DefaultRetryPolicy)
	_ = dlq.Put(context.Background(), DLQEntry{Envelope: Envelope{ID: "a1", Name: "t"}})
	_ = dlq.Put(context.Background(), DLQEntry{Envelope: Envelope{ID: "a2", Name: "t"}})
	n, _ := dlq.Purge(context.Background(), []string{"a1"})
	if n != 1 {
		t.Fatalf("purge a1 = %d, want 1", n)
	}
	if left, _ := dlq.Len(context.Background()); left != 1 {
		t.Errorf("dlq len after targeted purge = %d, want 1", left)
	}
}

func TestInMemoryDLQPutIdempotent(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	dlq := NewInMemoryDLQ(q, DefaultRetryPolicy)
	_ = dlq.Put(context.Background(), DLQEntry{Envelope: Envelope{ID: "x", Name: "t"}, Reason: "fatal"})
	_ = dlq.Put(context.Background(), DLQEntry{Envelope: Envelope{ID: "x", Name: "t"}, Reason: "fatal2"})
	if n, _ := dlq.Len(context.Background()); n != 1 {
		t.Errorf("idempotent put should not duplicate, len = %d", n)
	}
	entries, _ := dlq.List(context.Background(), 1)
	if entries[0].Reason != "fatal2" {
		t.Errorf("idempotent put should replace reason, got %s", entries[0].Reason)
	}
}

func TestSendToDLQ(t *testing.T) {
	q := NewInProcQueue(VisibilityOptions{}, nil)
	defer q.Close()
	dlq := NewInMemoryDLQ(q, DefaultRetryPolicy)
	env := &Envelope{ID: "z", Name: "t"}
	SendToDLQ(context.Background(), dlq, env, "test")
	if n, _ := dlq.Len(context.Background()); n != 1 {
		t.Errorf("SendToDLQ did not add, len = %d", n)
	}
	// nil DLQ should be a no-op (no panic).
	SendToDLQ(context.Background(), nil, env, "test")
}
