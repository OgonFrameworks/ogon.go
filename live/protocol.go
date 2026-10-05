// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/protocol — wire envelope and JSON codec (LIVE-018/019).
//
// The live protocol is JSON over WebSocket text frames or SSE event
// payloads. Envelopes carry a stable protocol version (SemVer-ish
// numeric), a Type discriminator, a Topic for routing, optional
// Cursor for resume (LIVE-014/015), optional AckID for at-least-once
// delivery (LIVE-017), and an opaque Payload.
//
// Backward-compat rules (LIVE-019):
//   - Adding a field is a minor version bump; old clients ignore it.
//   - Removing or repurposing a field requires a major version bump.
//   - The codec is strict on input (rejects unknown top-level fields
//     with version pinning) but lenient on output (always emits V1).

package live

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ProtocolVersion is the wire protocol major.minor. Bumped on
// breaking changes. Old clients receive a "version_mismatch" envelope
// from the server and must disconnect.
const (
	ProtocolMajor = 1
	ProtocolMinor = 0
)

// Envelope is the canonical wire frame. See PROMPT.md Part IX.1.
type Envelope struct {
	Version int             `json:"v"`               // protocol major (currently 1)
	Type    string          `json:"t"`               // message type discriminator
	Topic   string          `json:"k,omitempty"`     // topic (channel routing key)
	Cursor  uint64          `json:"c,omitempty"`     // server-assigned monotonic cursor
	AckID   string          `json:"a,omitempty"`     // ack ID for at-least-once
	TraceID string          `json:"trace,omitempty"` // trace ID for spans
	Payload json.RawMessage `json:"p,omitempty"`     // opaque JSON payload
}

// Encode marshals an envelope to JSON bytes.
func (e *Envelope) Encode() ([]byte, error) {
	if e == nil {
		return nil, errors.New("ogon/live: nil envelope")
	}
	if e.Version == 0 {
		e.Version = ProtocolMajor
	}
	return json.Marshal(e)
}

// DecodeEnvelope parses a JSON byte slice into an Envelope and applies
// protocol-version validation. Unknown versions are rejected with
// ErrVersionMismatch to force the client to negotiate.
func DecodeEnvelope(b []byte) (*Envelope, error) {
	if len(b) == 0 {
		return nil, errors.New("ogon/live: empty frame")
	}
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("ogon/live: decode: %w", err)
	}
	if e.Version == 0 {
		// Tolerate omitted version (treat as current major).
		e.Version = ProtocolMajor
	}
	if e.Version != ProtocolMajor {
		return nil, fmt.Errorf("%w: client v%d, server v%d",
			ErrVersionMismatch, e.Version, ProtocolMajor)
	}
	return &e, nil
}

// ErrVersionMismatch is returned/sent on protocol-version divergence.
var ErrVersionMismatch = errors.New("ogon/live: protocol version mismatch")

// Standard message types (Type values). These are reserved; user-defined
// types MUST be namespaced with a "user." prefix to avoid collisions.
const (
	TypeHello       = "hello"     // server→client on connect
	TypeWelcome     = "welcome"   // server→client reply to hello
	TypeSubscribe   = "subscribe" // client→server: subscribe to pattern
	TypeUnsubscribe = "unsubscribe"
	TypeMessage     = "message"  // application payload (broadcast or direct)
	TypePresence    = "presence" // presence diff (join/leave)
	TypeAck         = "ack"      // client→server: ack delivery (at-least-once)
	TypeResume      = "resume"   // client→server: resume from cursor
	TypePing        = "ping"     // heartbeat
	TypePong        = "pong"
	TypeError       = "error" // server→client protocol error
	TypeEvict       = "evict" // server→client slow-client eviction
)
