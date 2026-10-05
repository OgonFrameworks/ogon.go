// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/per_message_auth — per-message auth recheck hook (LIVE-024).
//
// Many frameworks authenticate only at WS upgrade time. OGON-LIVE
// requires a per-message recheck so a token revoked mid-session still
// stops further inbound messages. The recheck is application-defined
// (typically: parse a JWT from envelope.Headers["Authorization"],
// verify signature, check expiry, check revocation list) and runs on
// every inbound application message (not on protocol-control frames
// like ping/pong/subscribe).
//
// The hook returns:
//   - nil to allow
//   - a non-nil error to deny (the connection receives an error envelope
//     and may be force-closed after N denials)

package live

import "context"

// PerMessageAuth is invoked on every inbound application message.
type PerMessageAuth func(ctx context.Context, conn *Connection, env *Envelope) error

// defaultDenialCap: after this many consecutive denials, the connection
// is force-closed (it's almost certainly a misbehaving client).
const defaultDenialCap = 5

// checkPerMessage invokes the auth hook (if installed) and tracks
// denials. Returns nil if allowed; returns the denial error and
// triggers force-close once the cap is exceeded.
func (h *Hub) checkPerMessage(ctx context.Context, c *Connection, env *Envelope, hook PerMessageAuth) error {
	if hook == nil {
		return nil
	}
	if err := hook(ctx, c, env); err != nil {
		n := c.denials.Add(1)
		if n >= defaultDenialCap {
			h.log.Warn("per-message auth denial cap; evicting", "conn", c.ID, "denials", n)
			h.evict(c, "auth_denial_cap")
		}
		return err
	}
	c.denials.Store(0)
	return nil
}
