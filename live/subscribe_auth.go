// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/subscribe_auth — subscribe authorization (LIVE-045).
//
// Distinct from join hooks (LIVE-003) which fire on channel join,
// subscribe auth gates the *subscribe* envelope itself, before the
// channel registry is touched. This two-layer separation matters:
//   - join hooks are per-channel policy (room owner controls who joins)
//   - subscribe auth is per-connection policy (tenant controls which
//     channels a user may even attempt to subscribe to)
//
// SubscribeAuth receives (conn, pattern) and returns nil to allow or
// an error to deny. The error becomes an "error" envelope.

package live

import "context"

// SubscribeAuth gates pattern subscription attempts.
type SubscribeAuth func(ctx context.Context, conn *Connection, pattern string) error

// checkSubscribe runs the auth hook (if any).
func (h *Hub) checkSubscribe(ctx context.Context, c *Connection, pattern string, hook SubscribeAuth) error {
	if hook == nil {
		return nil
	}
	return hook(ctx, c, pattern)
}
