// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/reconnect_test — cursor-based resume.

package live

import (
	"testing"
)

func TestResumeWindowAppendAndReplay(t *testing.T) {
	w := NewResumeWindow(8, 0) // ttl=0 means default 30s in NewResumeWindow
	c1 := w.Append([]byte("a"))
	c2 := w.Append([]byte("b"))
	c3 := w.Append([]byte("c"))
	if c2 != c1+1 || c3 != c2+1 {
		t.Fatalf("cursors not monotonic: %d %d %d", c1, c2, c3)
	}
	out, err := w.Replay(c1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 events after cursor %d, got %d", c1, len(out))
	}
	if string(out[0]) != "b" || string(out[1]) != "c" {
		t.Fatalf("unexpected replay payloads: %s %s", out[0], out[1])
	}
}

func TestResumeWindowExpired(t *testing.T) {
	w := NewResumeWindow(2, 0)
	c1 := w.Append([]byte("a")) // cursor 1
	_ = w.Append([]byte("b"))   // cursor 2 — evicted by cap
	_ = w.Append([]byte("c"))   // cursor 3 — surviving
	_ = w.Append([]byte("d"))   // cursor 4 — surviving
	// window cap is 2 — cursors 1 and 2 are gone. Client last saw 1;
	// expected next cursor is 2 but that's gone → ErrResumeExpired.
	_, err := w.Replay(c1)
	if err == nil {
		t.Fatal("expected ErrResumeExpired")
	}
}

func TestResumeWindowFreshReplay(t *testing.T) {
	w := NewResumeWindow(8, 0)
	_ = w.Append([]byte("x"))
	_ = w.Append([]byte("y"))
	out, err := w.Replay(0)
	if err != nil {
		t.Fatalf("fresh replay: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 events fresh replay, got %d", len(out))
	}
}
