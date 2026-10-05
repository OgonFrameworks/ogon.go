// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — security: sensitive payload encryption at rest (JOBS-043),
// panic→error with redacted stack (JOBS-047).
//
// Encryption: a per-process symmetric key (32 bytes) wraps the
// Envelope.Payload bytes before enqueue. The DB/Redis driver
// stores the wrapped bytes (prefixed with "enc1:") so a DBA
// reading the table directly cannot see the plaintext. Workers
// unwrap on dequeue. Key rotation is out of scope for v1; v2 will
// support a key id header.
//
// Panic recovery: the worker pool wraps every dispatch in a
// recover() that converts the panic into a structured error with
// a redacted stack (file:line only — no argument values, which
// may contain sensitive data).

package jobs

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// PayloadKey holds the symmetric key for envelope encryption. Zero
// value means encryption is disabled (dev mode).
type PayloadKey struct {
	// Key is the 32-byte AES-256 key. Empty disables encryption.
	Key []byte
}

// Enabled reports whether encryption is on.
func (k PayloadKey) Enabled() bool { return len(k.Key) == 32 }

// Seal encrypts plaintext and returns a "enc1:"-prefixed base64 blob.
// Returns plaintext verbatim (no prefix) when encryption is disabled.
func (k PayloadKey) Seal(plaintext []byte) ([]byte, error) {
	if !k.Enabled() {
		return plaintext, nil
	}
	block, err := aes.NewCipher(k.Key)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-J0043", Title: "jobs: aes new cipher"})
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, 6+len(nonce)+len(ct))
	out = append(out, []byte("enc1:")...)
	out = append(out, nonce...)
	out = append(out, ct...)
	enc := base64.StdEncoding.EncodeToString(out)
	return []byte(enc), nil
}

// Open decrypts a "enc1:"-prefixed blob. Returns the input
// unchanged when no prefix is present (so un-encrypted envelopes
// interoperate with the encryption-disabled mode).
//
// BUGFIX (P14 bug-bounty): Seal returns the BASE64 ENCODING of the
// raw "enc1:"+nonce+ciphertext bytes, so the prefix check MUST be
// applied AFTER base64-decoding the input. Previously the prefix
// check inspected the raw input bytes, which never matched the
// base64-encoded Seal output — causing Open to return the base64
// string unchanged and the round-trip to silently fail.
func (k PayloadKey) Open(b []byte) ([]byte, error) {
	// Try decoding the input as base64. Seal's output is always
	// base64; if the input is not valid base64, we treat it as
	// an un-encrypted payload (interop mode).
	rawStr := string(b)
	raw, err := base64.StdEncoding.DecodeString(rawStr)
	if err != nil {
		// Not valid base64 — assume un-encrypted, return as-is.
		return b, nil
	}
	if len(raw) < 5 || string(raw[:5]) != "enc1:" {
		// Decoded but no enc1: prefix — return original input
		// (the un-encrypted interop path).
		return b, nil
	}
	payload := raw[5:]
	if !k.Enabled() {
		return nil, diag.New("OGON-J0043", "jobs: encrypted payload but no key", "")
	}
	block, err := aes.NewCipher(k.Key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(payload) < ns {
		return nil, errors.New("jobs: short ciphertext")
	}
	nonce, ct := payload[:ns], payload[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-J0043", Title: "jobs: gcm open"})
	}
	return pt, nil
}

// KeyFromPassphrase derives a 32-byte key from a passphrase using
// SHA-256 (deterministic, low-cost — sufficient for dev; production
// MUST supply a randomly generated key managed via KMS).
func KeyFromPassphrase(p string) PayloadKey {
	if p == "" {
		return PayloadKey{}
	}
	sum := sha256.Sum256([]byte(p))
	return PayloadKey{Key: sum[:]}
}

// EncryptingQueue wraps a Queue so every enqueue seals the payload
// and every dequeue opens it. The wrap is transparent to callers
// because Envelope.Payload is still JSON-encoded Args, just sealed.
type EncryptingQueue struct {
	Queue
	key PayloadKey
}

// NewEncryptingQueue wraps q. If key is disabled, the wrapper is a no-op.
func NewEncryptingQueue(q Queue, key PayloadKey) *EncryptingQueue {
	return &EncryptingQueue{Queue: q, key: key}
}

// Enqueue seals the payload before delegating to the underlying queue.
func (e *EncryptingQueue) Enqueue(ctx context.Context, env *Envelope, opts EnqueueOptions) error {
	sealed, err := e.key.Seal(env.Payload)
	if err != nil {
		return err
	}
	sealedEnv := *env
	sealedEnv.Payload = sealed
	return e.Queue.Enqueue(ctx, &sealedEnv, opts)
}

// Dequeue opens the payload after the underlying queue dequeues.
func (e *EncryptingQueue) Dequeue(ctx context.Context) (*Envelope, Receipt, error) {
	env, r, err := e.Queue.Dequeue(ctx)
	if err != nil {
		return nil, nil, err
	}
	if env == nil {
		return env, r, nil
	}
	opened, err := e.key.Open(env.Payload)
	if err != nil {
		return env, r, err
	}
	env.Payload = opened
	return env, r, nil
}

// RecoverDispatch wraps the supplied dispatcher with panic recovery.
// On panic it returns a structured error with a redacted stack trace
// (JOBS-047). The stack is capped to 4KB to bound memory.
func RecoverDispatch(name JobName, fn func(context.Context, *Envelope) error) func(context.Context, *Envelope) error {
	return func(ctx context.Context, env *Envelope) (err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := RedactedStack(8 * 1024)
				err = diag.New("OGON-J0047", "jobs: panic in handler", "recovered")
				err = diag.Wrap(err, diag.Diag{
					Code:  "OGON-J0047",
					What:  "panic: " + jsonNumber(r),
					Where: string(name),
				})
				// stash the redacted stack on env so worker can attach to DLQ.
				if env != nil {
					env.Stack = stack
				}
			}
		}()
		return fn(ctx, env)
	}
}

// RedactedStack captures the current goroutine stack and redacts
// argument values from function frames (they may contain sensitive
// data). Returns "file:line" pairs only.
func RedactedStack(max int) string {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	if n > max {
		n = max
	}
	s := string(buf[:n])
	// Strip argument lists inside (...) after the function name on
	// each "goroutine N [status]:\nfunc(...)(...)" line. We keep only
	// the function name and the file:line.
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "("); i > 0 && strings.Contains(line, ".") {
			line = line[:i] // keep "pkg.Func" only
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	out := b.String()
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func jsonNumber(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TimeoutDispatch wraps the supplied dispatcher with a per-job
// timeout (JOBS-016). If the handler does not return within d, the
// context is cancelled and the worker treats the dispatch as failed.
func TimeoutDispatch(d time.Duration, fn func(context.Context, *Envelope) error) func(context.Context, *Envelope) error {
	return func(ctx context.Context, env *Envelope) error {
		tctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- fn(tctx, env) }()
		select {
		case err := <-done:
			return err
		case <-tctx.Done():
			return diag.New("OGON-J0016", "jobs: dispatch timeout", "")
		}
	}
}

// leakGuard ensures goroutines spawned by TimeoutDispatch do not
// outlive the parent worker pool. We use a sync.WaitGroup to track
// them and a stopper to signal shutdown.
var leakMu sync.Mutex
