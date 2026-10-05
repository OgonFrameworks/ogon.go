// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — Redis list-based queue with visibility timeout (JOBS-003/015).
//
// Enqueue: LPUSH to "ogon:jobs:{queue}" (a JSON envelope).
// Dequeue: RPOP from the ready list (LIFO for cache-friendliness on
// dev workloads; FIFO is achievable via LMOVE to an "inflight" list
// instead — see dequeue implementation below).
//
// Visibility is implemented via a separate sorted set "ogon:jobs:{queue}:inflight"
// scored by lease-expiry timestamp. A reaper (StaleReaper) periodically
// scans this set for expired leases and LMOVEs them back to the ready list.
// On Ack the member is removed from the inflight set.
//
// This driver does not persist in-flight envelopes across a Redis
// restart (no AOF); for true durability use the DB driver.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/redis/go-redis/v9"
)

// RedisDriver is the Redis-backed queue. One driver per queue name.
type RedisDriver struct {
	client   *redis.Client
	key      string // ready list
	inflight string // inflight zset
	vis      VisibilityOptions
	enis     *Enforcer
}

// NewRedisDriver constructs a Redis queue driver. The supplied client
// MUST be pre-pinged by the caller (typically the live/pubsub init).
// queueName namespaces multiple queues within one Redis DB.
func NewRedisDriver(client *redis.Client, queueName string, vis VisibilityOptions, enf *Enforcer) *RedisDriver {
	if queueName == "" {
		queueName = "default"
	}
	vis = vis.WithDefaults()
	if enf == nil {
		enf = NewEnforcer(5 * time.Minute)
	}
	return &RedisDriver{
		client:   client,
		key:      "ogon:jobs:" + queueName,
		inflight: "ogon:jobs:" + queueName + ":inflight",
		vis:      vis,
		enis:     enf,
	}
}

// Enqueue implements Queue.
func (r *RedisDriver) Enqueue(ctx context.Context, env *Envelope, opts EnqueueOptions) error {
	if r.client == nil {
		return errors.New("jobs/redis: nil client")
	}
	if env.ID == "" {
		env.ID = NewID()
	}
	now := time.Now().UTC()
	if env.EnqueuedAt.IsZero() {
		env.EnqueuedAt = now
	}
	if !opts.VisibleAt.IsZero() {
		env.VisibleAt = opts.VisibleAt
	} else if env.VisibleAt.IsZero() {
		env.VisibleAt = now
	}
	if opts.MaxAttempts > 0 {
		env.MaxAttempts = opts.MaxAttempts
	}
	if opts.TenantID != "" {
		env.TenantID = opts.TenantID
	}
	env.Priority = opts.Priority
	if opts.IdempotencyKey != "" {
		env.IdempotencyKey = opts.IdempotencyKey
		if !r.enis.Claim(opts.IdempotencyKey) {
			return diag.New("OGON-J0014", "jobs: idempotency collision", "key in flight")
		}
	}
	b, err := json.Marshal(env)
	if err != nil {
		r.enis.Release(opts.IdempotencyKey)
		return diag.Wrap(err, diag.Diag{Code: "OGON-J0030", Title: "jobs/redis: encode envelope"})
	}
	// delayed enqueue: schedule via a per-driver zset; for simplicity
	// we LPUSH immediately — delayed jobs are a DB-driver feature and
	// the Redis driver is optimised for at-most-now workloads.
	if err := r.client.LPush(ctx, r.key, b).Err(); err != nil {
		r.enis.Release(opts.IdempotencyKey)
		return diag.Wrap(err, diag.Diag{Code: "OGON-J0031", Title: "jobs/redis: lpush"})
	}
	return nil
}

// Dequeue implements Queue. Uses BRPOP with a short timeout to honour
// context cancellation. On success, records the lease in the inflight
// sorted set.
func (r *RedisDriver) Dequeue(ctx context.Context) (*Envelope, Receipt, error) {
	if r.client == nil {
		return nil, nil, errors.New("jobs/redis: nil client")
	}
	timeout := 1 * time.Second
	for {
		if ctx.Err() != nil {
			return nil, nil, ErrEmpty
		}
		result, err := r.client.BRPop(ctx, timeout, r.key).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return nil, nil, ErrEmpty
			}
			return nil, nil, err
		}
		if len(result) < 2 {
			continue
		}
		// result[0] is the key name; result[1] is the value.
		val := result[1]
		var env Envelope
		if err := json.Unmarshal([]byte(val), &env); err != nil {
			// poison — drop and continue (JOBS-025). We cannot route
			// to a real DLQ without a queue reference, so we drop.
			continue
		}
		env.Attempts++
		lease := r.vis.TimeoutFor(env.Name)
		expireAt := time.Now().Add(lease).UnixMilli()
		// store the (possibly re-marshalled) envelope in the inflight
		// zset so StaleReaper can requeue it on expiry.
		b, _ := json.Marshal(env)
		if err := r.client.ZAdd(ctx, r.inflight, redis.Z{
			Score:  float64(expireAt),
			Member: b,
		}).Err(); err != nil {
			// requeue immediately on failure
			_ = r.client.LPush(ctx, r.key, b).Err()
			return nil, nil, err
		}
		rcpt := ReceiptMeta{ID: env.ID, Token: val}
		return &env, rcpt, nil
	}
}

// Ack implements Queue — remove from inflight zset by envelope id.
func (r *RedisDriver) Ack(ctx context.Context, rcpt Receipt) error {
	// scan inflight for matching id (id is inside the JSON envelope).
	return r.removeFromInflight(ctx, rcpt.EnvelopeID())
}

// Nack implements Queue.
func (r *RedisDriver) Nack(ctx context.Context, rcpt Receipt, requeue bool, nextVisibleAt time.Time, lastErr string) error {
	if err := r.removeFromInflight(ctx, rcpt.EnvelopeID()); err != nil {
		return err
	}
	if !requeue {
		return nil
	}
	// push back to ready list with the new visible_at stamped in
	// the envelope. The ready list is LIFO so the next Dequeue will
	// pick this up — we re-encode the envelope to reflect the delay
	// by writing it to the inflight zset with score=nextVisibleAt
	// and a special member id that the reaper picks up.
	env := Envelope{
		ID:        rcpt.EnvelopeID(),
		LastError: lastErr,
		VisibleAt: nextVisibleAt,
	}
	b, _ := json.Marshal(env)
	return r.client.ZAdd(ctx, r.inflight, redis.Z{
		Score:  float64(nextVisibleAt.UnixMilli()),
		Member: b,
	}).Err()
}

// Depth implements Queue.
func (r *RedisDriver) Depth(ctx context.Context) (int64, error) {
	n1, err := r.client.LLen(ctx, r.key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	n2, err := r.client.ZCard(ctx, r.inflight).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	return n1 + n2, nil
}

// Peek implements Queue. Returns the ready list contents.
func (r *RedisDriver) Peek(ctx context.Context, n int) ([]*Envelope, error) {
	if n <= 0 {
		n = 50
	}
	vals, err := r.client.LRange(ctx, r.key, 0, int64(n-1)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]*Envelope, 0, len(vals))
	for _, v := range vals {
		var env Envelope
		if err := json.Unmarshal([]byte(v), &env); err == nil {
			out = append(out, &env)
		}
	}
	return out, nil
}

// Close implements Queue — does NOT close the client (shared with
// pubsub and other subsystems).
func (r *RedisDriver) Close() error { return nil }

// removeFromInflight walks the inflight zset and removes any member
// whose envelope ID matches. O(N) on inflight size; fine for the
// modest sizes expected per driver instance.
func (r *RedisDriver) removeFromInflight(ctx context.Context, id string) error {
	members, err := r.client.ZRangeWithScores(ctx, r.inflight, 0, -1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	for _, m := range members {
		s, ok := m.Member.(string)
		if !ok {
			continue
		}
		var env Envelope
		if err := json.Unmarshal([]byte(s), &env); err != nil {
			continue
		}
		if env.ID == id {
			if err := r.client.ZRem(ctx, r.inflight, s).Err(); err != nil && !errors.Is(err, redis.Nil) {
				return err
			}
			return nil
		}
	}
	return nil
}

// SweepReaper requeues inflight members whose lease has expired.
// Called periodically by StaleReaper via a hook (we expose it here
// because the Redis driver is authoritative for its own inflight set).
//
// SweepReaper is best-effort: partial failures are retried next tick.
func (r *RedisDriver) SweepReaper(ctx context.Context, now time.Time) (int, error) {
	members, err := r.client.ZRangeByScore(ctx, r.inflight, &redis.ZRangeBy{
		Min: "-inf", Max: strconv.FormatInt(now.UnixMilli(), 10),
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	count := 0
	for _, m := range members {
		var env Envelope
		if err := json.Unmarshal([]byte(m), &env); err != nil {
			continue
		}
		// requeue: LPUSH the envelope back to ready and remove from inflight
		if err := r.client.LPush(ctx, r.key, m).Err(); err != nil {
			continue
		}
		if err := r.client.ZRem(ctx, r.inflight, m).Err(); err != nil && !errors.Is(err, redis.Nil) {
			continue
		}
		count++
	}
	return count, nil
}
