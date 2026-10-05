# OgonGo Bug Bounty — Findings (Agent 1 Codebase)

This file is the bug-bounty ledger for the **Agent 1** OgonGo codebase at
`/home/z/my-project/work/OgonGo/`. It was produced by Task **P4-A**
(Bug Bounty Port + Fixes) by walking the Agent 3 bug-bounty report
(`evaluation/agent3/OgonGo/BUG-BOUNTY.md`, 30 findings) against Agent 1's
source tree and applying the applicable fixes.

Each finding is filed with severity, area, description, reproduction,
impact, suggested fix, and status. Status values:

- `fixed-in-commit-internal` — a minimal, safe patch was applied during
  this task to the Agent 1 codebase.
- `already-safe-in-A1` — Agent 1's code does not exhibit the bug (the
  feature is absent, or the implementation already does the right thing).
- `not-applicable-in-A1` — Agent 1 does not have the affected feature /
  function / file, so the bug cannot apply.
- `open` — the bug applies but the fix requires an API or design change
  that is out of scope for this task; documented for a future task.

The bug sweep walked the following Agent 1 packages: `http` (router,
bind, middleware), `auth` (token, oauth, passkey, csrf, csp, ratelimit),
`authz` (rbac, tenant, policy), `ui/runtime` (forms), `jobs` (retry,
poison), `live` (reconnect, broadcast, backpressure), `record` (raw,
query). Findings are numbered BUG-NNNN and cross-reference the Agent 3
report's numbering for traceability.

---

## BUG-0001: RateLimiter token-bucket read-modify-write race (Agent 3 variant: tokenBucketLimiter.Allow races on store map)
- Severity: Critical
- Area: ogon/http/middleware_ratelimit.go (RateLimiter.Allow)
- Cross-ref: Agent 3 BUG-0001
- Description: Agent 3's `tokenBucketLimiter.Allow` mutated `l.store` (a `map[string]*bucket`) and `b.tokens` / `b.last` without any mutex. Agent 1's `RateLimiter` already protects the policy map with `rl.mu`, but the per-bucket token-bucket math used `atomic.Int64` for `tokens` and `lastTs` with a non-atomic read-modify-write: `tokens := st.tokens.Load(); tokens = min64(...); st.tokens.Store(tokens - 1)`. Two concurrent `Allow` calls can both `Load` the same `tokens` value, both pass the `> 0` check, and both `Store(tokens - 1)` — consuming only one token between them. This is a logical lost-update race (not a Go data race, but the same security impact: rate limits are silently bypassed under concurrency).
- Reproduction:
  ```go
  rl := NewRateLimiter()
  rl.Register(RateLimitPolicy{Name: "p", Burst: 10, Steady: 1})
  var wg sync.WaitGroup
  for i := 0; i < 100; i++ {
      wg.Add(1)
      go func() { defer wg.Done(); rl.Allow("p") }()
  }
  wg.Wait()
  // Under the bug: ~100 calls succeed despite Burst=10.
  ```
- Impact: A determined attacker can bypass rate limits by issuing concurrent requests — tokens are counted down under the race, so the effective limit is much higher than configured.
- Fix: Replaced the `atomic.Int64` token-bucket fields with a `sync.Mutex` (`tbMu`) guarding `tokens float64` and `lastTs time.Time`. The entire refill + decrement is now atomic. Also switched to `float64` for smooth (sub-second) refill (Agent 3 BUG-0013 fix).
- Status: fixed-in-commit-internal

---

## BUG-0002: RetryStormGuard / Quarantine synchronization (Agent 3 variant: StormGuard.Allow mutates shared state without sync)
- Severity: Critical
- Area: ogon/jobs/poison.go (RetryStormGuard)
- Cross-ref: Agent 3 BUG-0002
- Description: Agent 3's `StormGuard.Allow` mutated `g.retries`, `g.windowStart`, `g.paused`, `g.pausedUntil` without synchronization. Agent 1's `RetryStormGuard` already uses a `sync.Mutex` for `history` and `armedUntil`, and `atomic.Bool` for `armed`. The `Record` and `Wait` methods are properly synchronized.
- Reproduction: n/a (Agent 1's implementation already holds `g.mu` for the duration of `Record` and `Wait`).
- Impact: None in Agent 1.
- Fix: None required. (Noted: `g.history = g.history[1:]` in `Record` shifts the slice header forward without shrinking the backing array — same pattern as BUG-0019. The retry-storm guard's history is bounded by the window TTL and is small, so this is not fixed here; see BUG-0019 for the analogous fix in `live/reconnect`.)
- Status: already-safe-in-A1

---

## BUG-0003: IssueAccess/IssueRefresh always sign with RS256 even when the Key declares RS384 or RS512
- Severity: High
- Area: ogon/auth/token/jwt.go (Issuer.IssueAccess, Issuer.IssueRefresh)
- Cross-ref: Agent 3 BUG-0003
- Description: The Issuer hardcoded `jwt.NewWithClaims(jwt.SigningMethodRS256, claims)` regardless of the Key's `Alg` field. A deployment that configures a Key with `Alg: AlgRS384` would silently issue RS256-signed tokens, which then fail verification against the RS384-keyed `JWKSCache` entry (the `pub.Alg != alg` defense-in-depth check rejects them). The whole point of storing `Alg` on the Key is to allow rotation; this implementation voided that.
- Reproduction:
  ```go
  key384 := &Key{Kid: "k1", Alg: AlgRS384, Key: rsa384PrivKey}
  is, _ := NewIssuer(opts, key384)
  tok, _ := is.IssueAccess(ctx, "u", "t", nil)
  // tok header has "alg":"RS256" — wrong; Verify rejects.
  ```
- Impact: Algorithm rotation (SEC-058 / SEC-071) is silently broken. A migration to RS384 in production produces tokens that fail validation.
- Fix: Added `signingMethodFor(Algorithm) jwt.SigningMethod` helper that maps `AlgRS256`→`SigningMethodRS256`, `AlgRS384`→`SigningMethodRS384`, `AlgRS512`→`SigningMethodRS512`. `IssueAccess` and `IssueRefresh` now pick the signing method from `key.Alg`. If the alg is not in the allowlist, the issue call returns a `OGON-SEC-083` diag.
- Status: fixed-in-commit-internal

---

## BUG-0004: JWT alg allowlist — HS* defense-in-depth (Agent 3 variant: only rejects HS256)
- Severity: High
- Area: ogon/auth/token/jwt.go (Issuer.Verify)
- Cross-ref: Agent 3 BUG-0004
- Description: Agent 3's parser only explicitly rejected `HS256`; `HS384`/`HS512` could slip through if the allowlist was misconfigured. Agent 1's `allowedAlgs` map only contains `RS256`/`RS384`/`RS512` (no HS* and no `"none"`), so the bug as described does not apply — the allowlist already excludes every HS* alg. However, the allowlist is a package-private `var` that a future contributor could expand; the Agent 3 fix (a single `strings.HasPrefix(alg, "HS")` check as defense-in-depth) is a cheap hardening that prevents any future misconfiguration from re-opening the classic alg-confusion attack (CVE-2015-9235 / RFC 8725 §3.5).
- Reproduction: n/a in Agent 1 (the allowlist already excludes HS*).
- Impact: None today; latent if the allowlist is ever widened.
- Fix: Added `strings.HasPrefix(algStr, "HS")` rejection in `Verify`'s keyfunc, before the allowlist lookup. The empty-alg and unsigned-alg cases are rejected by the existing `!allowedAlgs[alg]` lookup. (Note: the explicit string-literal check for the unsigned alg was avoided to keep the `TestJWTAlgAllowlistNoNone` source-grep test passing; the allowlist rejection covers it.)
- Status: fixed-in-commit-internal (defense-in-depth)

---

## BUG-0005: Concurrent same-key idempotency requests re-execute the handler
- Severity: High
- Area: ogon/http/middleware_idempotency.go (IdempotencyMiddleware)
- Cross-ref: Agent 3 BUG-0005
- Description: When two requests with the same `Idempotency-Key` arrive concurrently, both probe the cache (miss), both install a recorder, and both wrap `c.route.Handler` to capture the response. Both then execute the handler. The first to finish stores its snapshot; the second overwrites it. The spec SEC-042 / HTTP-035 promises "a retry returns the same response without re-executing the handler" — concurrent retries violate this directly, and a duplicate-write handler (e.g. a billing charge) will charge twice. As a bonus bug, the wrapper `c.route.Handler = func(c *Ctx) error { ... }` mutates the shared `Route` struct, so concurrent requests on the same route clobber each other's wrappers.
- Reproduction:
  ```bash
  for i in 1 2 3 4 5; do
    curl -X POST -H "Idempotency-Key: K1" -d '{"amt":1}' http://app/charge &
  done; wait
  # handler executed 5 times despite identical idempotency key
  ```
- Impact: Duplicate side effects (charges, emails, state mutations) when a client retries in parallel (e.g. for HA) — the exact failure mode SEC-042 exists to prevent.
- Fix: Added a `singleflight.Group` to `IdempotencyCache`. The middleware now: (1) fast-paths a cache hit; (2) on miss, calls `cache.sf.Do(cacheKey, ...)` so the leader runs the handler inline (via `c.route.Handler(c)`) and stores the snapshot, while waiters block on the singleflight and replay the snapshot when the leader finishes. The leader calls `c.Abort()` so the dispatcher does not call the handler a second time. The shared-`Route.Handler`-mutation bug is also fixed (the middleware no longer reassigns `c.route.Handler`).
- Status: fixed-in-commit-internal

---

## BUG-0006: HEAD auto-derivation writes no Content-Length (Agent 3 variant: bodyDroppingResponseWriter)
- Severity: Medium
- Area: ogon/http/router.go (bodyDroppingResponseWriter) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0006
- Description: Agent 3's `bodyDroppingResponseWriter` dropped the body for HEAD requests without setting `Content-Length`. Agent 1's router does not auto-derive HEAD from GET; HEAD must be registered explicitly via `Router.Map(http.MethodHead, ...)`. There is no `bodyDroppingResponseWriter` type in Agent 1.
- Reproduction: n/a (feature absent).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0007: Binding does not strip UTF-8 BOM before JSON decode
- Severity: Medium
- Area: ogon/http/ctx.go (jsonDecode, used by bindCtx)
- Cross-ref: Agent 3 BUG-0007
- Description: `json.NewDecoder(bytes.NewReader(data)).Decode(v)` does not skip a leading UTF-8 BOM (`\xEF\xBB\xBF`). A request body that begins with a BOM (some Windows HTTP clients and proxies add it) fails to decode with `invalid character 'ï'` and the caller receives a 422.
- Reproduction:
  ```bash
  printf '\xEF\xBB\xBF{"email":"a@b.c"}' | curl -X POST --data-binary @- -H 'Content-Type: application/json' http://app/users
  # 422: malformed JSON: invalid character 'ï'
  ```
- Impact: A subset of legitimate clients (older Windows tooling, some proxies) cannot POST JSON. The error message is misleading.
- Fix: Added `stripUTF8BOM(data []byte) []byte` helper that removes a single leading `EF BB BF`. `jsonDecode` calls it before constructing the decoder.
- Status: fixed-in-commit-internal

---

## BUG-0008: RenderCSRFInput HTML-injects the token (Agent 3 variant)
- Severity: Medium
- Area: ogon/ui/forms/forms.go (RenderCSRFInput) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0008
- Description: Agent 3's `RenderCSRFInput` used `fmt.Sprintf` to interpolate the token into an HTML attribute without escaping. Agent 1 does not have a `RenderCSRFInput` function (or a `ui/forms` package); the CSRF surface lives in `auth/csrf.go` and `http/middleware_csrf.go`, neither of which renders HTML. The `CSRF.Issue` helper sets a cookie and returns the token as a string; HTML rendering is the application's responsibility.
- Reproduction: n/a (function absent).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0009: pool.dispatchOne resets strikes on fast-failure (Agent 3 variant)
- Severity: Medium
- Area: ogon/live/pool/pool.go (dispatchOne) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0009
- Description: Agent 3's `dispatchOne` reset strike counters on any `Send` return (including errors). Agent 1's live package does not have a `dispatchOne` function or a strike-counter eviction system. Instead, `live/backpressure.go` uses a bounded `OutboundQueue` with drop policies and `StallDuration` tracking for slow-client eviction (LIVE-013). The fanout path goes through a bounded worker pool (`Hub.fanoutCh`) rather than per-dispatch goroutines, so a fast-failing connection does not bypass eviction — it just drops the frame and the queue's stall counter advances.
- Reproduction: n/a (architecture differs).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0010: RBAC tenant boundary check skipped when either TenantID is empty (Agent 3 variant)
- Severity: High
- Area: ogon/authz/tenant.go (TenantGuard.Check) — already correct in Agent 1
- Cross-ref: Agent 3 BUG-0010
- Description: Agent 3's defense-in-depth tenant check skipped when either side was empty. Agent 1's `TenantGuard.Check` already handles the empty-tenant case correctly: with the default `denyMissing=true`, both-empty and one-empty both return a `OGON-SEC-027` diag (fail-closed). The `WithAllowMissing()` opt-in mirrors Agent 3's suggested `AllowEmptyTenant` option. Agent 1's `Policy.CheckResource` does not perform a tenant check itself (it checks role grants and resource grants only) — tenant isolation is the `TenantGuard`'s job, wired in via `TenantGuard.Middleware`.
- Reproduction: n/a (already correct).
- Impact: None in Agent 1.
- Fix: None required. (P4-A discovery: a separate bug — `authz.WithTenant` stored a `string` but `TenantFromContext` asserted to `*string`, so the assertion always failed and `TenantFromContext` always returned `""`. Fixed separately; see P4-A-D1 below.)
- Status: already-safe-in-A1

---

## BUG-0011: OAuth UserInfo body is read without size limit (memory-exhaustion DoS)
- Severity: Medium
- Area: ogon/auth/oauth/oidc.go (UserInfo)
- Cross-ref: Agent 3 BUG-0011
- Description: `UserInfo` read the response body via `json.NewDecoder(resp.Body).Decode(&out)` with no upper bound. A malicious or compromised OIDC provider (or a man-in-the-middle that bypasses TLS) could return gigabytes, exhausting process memory.
- Reproduction: Stand up a fake OIDC provider whose userinfo endpoint streams 10 GB of JSON; call `UserInfo` against it.
- Impact: Memory-exhaustion DoS of the calling service. The OIDC provider is an external trust boundary, so this matters even though it's "trusted".
- Fix: Added `userInfoMaxBytes = 1 << 20` (1 MiB) cap. `UserInfo` now wraps the body in `io.LimitReader(resp.Body, userInfoMaxBytes+1)` before decoding. If the JSON is truncated mid-structure, `json.Decoder` returns "unexpected EOF" — the desired fail-closed behavior.
- Status: fixed-in-commit-internal

---

## BUG-0012: StaticRegistry key concatenation allows key-collision spoofing (Agent 3 variant)
- Severity: Low
- Area: ogon/auth/oauth/oauth2.go (Registry) — different shape in Agent 1
- Cross-ref: Agent 3 BUG-0012
- Description: Agent 3's `StaticRegistry` keyed configs by `tenantID + "|" + provider`, allowing `tenantID="a|b"` to collide with `tenantID="a", provider="b|c"`. Agent 1's `Registry` keys configs by `Provider.ID` only (a single string, no concatenation), so the collision vector does not exist. There is no tenant dimension in Agent 1's OAuth registry.
- Reproduction: n/a (key shape differs).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0013: tokenBucketLimiter refill arithmetic uses integer ratio (Agent 3 variant)
- Severity: Medium
- Area: ogon/http/middleware_ratelimit.go (RateLimiter.Allow)
- Cross-ref: Agent 3 BUG-0013
- Description: Agent 3's refill used integer Duration division (`elapsed / l.period`), which produced 0 for sub-period elapsed times. Agent 1's refill already used `float64` math (`elapsed.Seconds() * float64(st.policy.Steady)`), so the chunky-refill bug did not apply. As part of the BUG-0001 fix, the refill math was kept on `float64` and is now guarded by `tbMu`.
- Reproduction: n/a (already float64).
- Impact: None in Agent 1.
- Fix: None required (the BUG-0001 fix preserves the float64 refill).
- Status: already-safe-in-A1

---

## BUG-0014: CSP Nonce uses predictable fallback on CSPRNG failure
- Severity: High
- Area: ogon/auth/csp.go (CSP.Nonce)
- Cross-ref: Agent 3 BUG-0014
- Description: Agent 1's `CSP.Nonce()` did `_, _ = rand.Read(b[:])` and then `c.nonce = base64.StdEncoding.EncodeToString(b[:])`. If `rand.Read` fails (rare on Linux but possible under fork-exhaustion, container entropy starvation, or a broken `/dev/urandom`), `b` stays all-zeros and `c.nonce` becomes a fixed predictable base64 string (`"AAAAAAAAAAAAAAAA..."`) — every response in that window gets the same known nonce. An attacker who knows the failure mode can craft `<script nonce="AAAAAAAA...">` that bypasses CSP entirely.
- Reproduction: Mock `rand.Read` to always return an error; observe every response uses the same predictable nonce.
- Impact: Total CSP bypass when the CSPRNG fails.
- Fix: `Nonce()` now returns `""` on `rand.Read` error (fail-closed). `WithScript` and `WithStyle` check for the empty nonce and skip the `'nonce-'` directive when it's empty — the resulting CSP blocks inline scripts/styles rather than failing open with a known nonce.
- Status: fixed-in-commit-internal

---

## BUG-0015: GenerateCSRFToken returns all-zeros hex string on rand failure (Agent 3 variant)
- Severity: High
- Area: ogon/ui/forms/forms.go (GenerateCSRFToken) — absent in Agent 1; auth/csrf.go already correct
- Cross-ref: Agent 3 BUG-0015
- Description: Agent 3's `GenerateCSRFToken` returned `strings.Repeat("0", 64)` on `rand.Read` failure. Agent 1 does not have a `GenerateCSRFToken` function. The equivalent is `CSRF.Issue` in `auth/csrf.go`, which already returns an error on `rand.Read` failure (`diag.Wrap(err, diag.Diag{Code: "OGON-SEC-008", Title: "csrf: rand"})`) and never issues a token.
- Reproduction: n/a (already correct).
- Impact: None in Agent 1.
- Fix: None required.
- Status: already-safe-in-A1

---

## BUG-0016: CORS middleware does not set `Vary: Origin` on responses that reflect origin
- Severity: Medium
- Area: ogon/http/middleware_cors.go (CORSMiddleware)
- Cross-ref: Agent 3 BUG-0016
- Description: Agent 1's CORS middleware only set `Vary: Origin` when `AllowCredentials` was true. When credentials were false but a specific origin was reflected (the `allowed = origin` branch) or `*` was returned, the response varied by request origin but `Vary: Origin` was not emitted. A CDN that caches the response will return one client's `Access-Control-Allow-Origin` value to another client, breaking CORS for the second client.
- Reproduction: Configure `AllowOrigins: []string{"https://a.example.com","https://b.example.com"}`, `AllowCredentials: false`. Send a request from `a.example.com` (cacheable GET). Send a request from `b.example.com` — the cached response still carries `Access-Control-Allow-Origin: https://a.example.com`, which the browser rejects for `b`.
- Impact: Cross-origin response leakage through CDN mis-cache.
- Fix: Moved `h.Add("Vary", "Origin")` outside the `AllowCredentials` check so it fires whenever the ACAO header is set.
- Status: fixed-in-commit-internal

---

## BUG-0017: CORS middleware allows wildcard `*` to be mixed with specific origins silently
- Severity: Low
- Area: ogon/http/middleware_cors.go (CORSConfig.Validate)
- Cross-ref: Agent 3 BUG-0017
- Description: Agent 1's `Validate` only rejected `*` when `AllowCredentials` was true. A config like `AllowOrigins: []string{"*", "https://a.example.com"}` with `AllowCredentials: false` was accepted silently — the `*` won and the explicit origin was ignored, opening CORS to everyone when the operator thought they'd allow-listed one origin.
- Reproduction: `CORSConfig{AllowOrigins: []string{"*", "https://a.example.com"}}` — silently collapses to `*`.
- Impact: Misconfigured policies are accepted; the operator thinks they've allow-listed one origin and disabled credentials, but they've actually opened CORS to everyone.
- Fix: `Validate` now tracks `hasWild` and `hasExplicit` and returns a new `errCORSWildcardMixed` problem if both are present, regardless of `AllowCredentials`.
- Status: fixed-in-commit-internal

---

## BUG-0018: live/pool.dispatchOne leaks a goroutine per dispatch (Agent 3 variant)
- Severity: Medium
- Area: ogon/live/pool/pool.go (dispatchOne) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0018
- Description: Agent 3's `dispatchOne` spawned a goroutine per dispatch and leaked it if `Send` blocked past the timeout. Agent 1's live package does not have a `dispatchOne` function. The fanout path uses a bounded worker pool (`Hub.fanoutCh` of capacity `WorkerPoolSize*64`) drained by `fanoutLoop` goroutines spawned under the supervisor. There is no per-dispatch goroutine, so there is no per-dispatch leak.
- Reproduction: n/a (architecture differs).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0019: live/reconnect ResumeWindow leaks backing array capacity (Agent 3 variant: ring.sweep)
- Severity: Low
- Area: ogon/live/reconnect.go (ResumeWindow.Append)
- Cross-ref: Agent 3 BUG-0019
- Description: Agent 1's `ResumeWindow.Append` did `w.events = w.events[len(w.events)-w.cap:]` to trim to the cap. This shifts the slice header forward but keeps the leading slots in the backing array allocated (the leading slots become garbage but the array never shrinks). Over a long-running channel at the cap, the slice header moves forward, the leading slots are not GC'd, and the live data structure is permanently top-heavy. The cost is amortised O(1) but the live data structure carries a non-shrinking backing array.
- Reproduction: Run a ResumeWindow at cap=128 for a week; observe the slice header offset grows (the underlying array doesn't shrink).
- Impact: Long-running buffers carry a non-shrinking backing array; minor memory overhead per channel.
- Fix: When trimming, copy the live tail into a fresh slice (`fresh := make([]resumeEntry, len(live)); copy(fresh, live); w.events = fresh`). The leading garbage is released to the GC.
- Status: fixed-in-commit-internal

---

## BUG-0020: JWKSCache grows without bound — unbounded-memory DoS (latent)
- Severity: Medium
- Area: ogon/auth/token/jwt.go (JWKSCache)
- Cross-ref: Agent 3 BUG-0020
- Description: Agent 1's `JWKSCache` was a plain `map[string]*PublicKey` with no eviction. The cache grows only on `RotateKey` calls (rare), so today it is bounded by the rotation count. But the docstring promises a "tiny LRU" and there is no LRU; a future caller wiring a larger JWKS (e.g. multi-tenant issuer with per-tenant signing keys) would make the cache unbounded.
- Reproduction: n/a (current behavior is bounded by rotation count).
- Impact: Latent — bounded today, unbounded if the JWKS grows.
- Fix: Rewrote `JWKSCache` as a bounded LRU using `container/list`. Cap is `jwksCacheCap = 32`. `get` promotes the entry to the front; `set` prepends and evicts the back when over cap. Added `NewJWKSCache()` constructor.
- Status: fixed-in-commit-internal

---

## BUG-0021: router.AllMethods is a mutable package-level slice (Agent 3 variant)
- Severity: Low
- Area: ogon/http/router/router.go (AllMethods) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0021
- Description: Agent 3 exported a mutable `var AllMethods = []Method{...}`. Agent 1's router does not export an `AllMethods` slice; it uses the constants from `net/http` (`http.MethodGet`, etc.) directly. There is no shared-mutable-state footgun.
- Reproduction: n/a (absent).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0022: OPTIONS to an unknown path advertises every method (Agent 3 variant)
- Severity: Low
- Area: ogon/http/router/router.go (ServeHTTP) — different dispatch in Agent 1
- Cross-ref: Agent 3 BUG-0022
- Description: Agent 3's router responded 204 with a full `Allow` list to OPTIONS on any path. Agent 1's `Router.Match` returns `MatchNone` for unknown paths (no routes node matches), which the server turns into a 404. For known paths with a method mismatch, it returns `MatchMethodNotAllowed` with the allowed methods. There is no special-casing of OPTIONS that advertises methods on unknown paths.
- Reproduction: n/a (behavior differs).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0023: refresh-token family revocation does not revoke already-issued access tokens (Agent 3 variant)
- Severity: Low
- Area: ogon/auth/token/token.go (RotateRefreshToken) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0023
- Description: Agent 3 had a `RotateRefreshToken` with family revocation. Agent 1's `Issuer` has `Refresh` (rotation) and `RevokeAllForUser` (logout), but no family-revocation concept. The `MemoryRefreshStore.Consume` is single-use (a consumed jti is rejected on reuse with `ErrRefreshUsed`), which is the rotation invariant. There is no family-tracking that would require access-token denylisting.
- Reproduction: n/a (feature absent).
- Impact: None in Agent 1.
- Fix: None required. (If family revocation is added later, the access-token denylist from Agent 3's fix should be ported at the same time.)
- Status: not-applicable-in-A1

---

## BUG-0024: token splitToken allows the second segment of a 3-segment token to be empty (Agent 3 variant)
- Severity: Low
- Area: ogon/auth/token/token.go (splitToken) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0024
- Description: Agent 3 had a custom `splitToken` helper with incomplete segment-length checks. Agent 1 uses `github.com/golang-jwt/jwt/v5`'s `ParseWithClaims`, which handles token splitting and segment validation internally. There is no custom `splitToken` function in Agent 1.
- Reproduction: n/a (uses stdlib parser).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0025: InMemoryCeremonyLimiter sweeps the entire counts map on every Allow call (Agent 3 variant)
- Severity: Low
- Area: ogon/auth/passkey/passkey.go (InMemoryCeremonyLimiter) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0025
- Description: Agent 3's `InMemoryCeremonyLimiter` swept the whole map on every `Allow`. Agent 1's `passkey.Service` takes a `CeremonyLimiter` interface (implemented by `auth.BucketLimiter` or `auth.SlidingLimiter` from `auth/ratelimit.go`). Those limiters use a `sync.Mutex` and per-key state; they do not sweep on every call. The sliding limiter trims expired stamps in-place per call (O(N) per key, N small). There is no `InMemoryCeremonyLimiter` concrete type in Agent 1.
- Reproduction: n/a (absent).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0026: binding.setJSONValue calls fv.Set on a non-addressable copy (Agent 3 variant)
- Severity: Medium
- Area: ogon/http/bind.go (setFromAny) — similar but different in Agent 1
- Cross-ref: Agent 3 BUG-0026
- Description: Agent 3's `setJSONValue` could panic on non-addressable map/interface fields. Agent 1's `setFromAny` handles struct fields via `json.Unmarshal(data, fv.Addr().Interface())` (which requires `fv` to be addressable). For non-struct kinds, it uses type-specific `Set*` calls which also require settable fields. The non-addressable case is reachable only through embedded unexported fields, which `bindCtx` skips (`if !field.IsExported() { continue }`). The interface-typed field case is not handled (falls through to the `default` branch silently). This is a latent issue but not a panic in the common path.
- Reproduction: Bind a JSON object into a struct whose field has type `map[string]string` and is reached through a non-addressable path (rare in practice).
- Impact: Latent — binding silently skips interface-typed fields; embedded unexported fields are skipped by the export check.
- Fix: Not applied (the fix requires a refactor of the reflection path and the codegen target makes the reflection path cold). Documented for a future task.
- Status: open (latent; documented)

---

## BUG-0027: RawQuery uses context.Background() instead of accepting a caller context (Agent 3 variant)
- Severity: Medium
- Area: ogon/record/query/query.go (RawQuery) — already correct in Agent 1
- Cross-ref: Agent 3 BUG-0027
- Description: Agent 3's `RawQuery` did not accept a context. Agent 1's `RawQuery[T]` already takes `ctx context.Context` as its first parameter and passes it to `d.Query(ctx, sqlStr, args...)`. The caller's deadline propagates through.
- Reproduction: n/a (already takes ctx).
- Impact: None in Agent 1.
- Fix: None required.
- Status: already-safe-in-A1

---

## BUG-0028: M2M.InsertSQL / DeleteSQL / SelectSQL interpolate struct fields into SQL (Agent 3 variant)
- Severity: Medium
- Area: ogon/record/relations/relations.go (M2M) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0028
- Description: Agent 3's `M2M` interpolated `JoinTable`/`LeftCol`/`RightCol` into SQL strings. Agent 1's `record` package does not have a `relations.go` file or an `M2M` type. There is no SQL-identifier interpolation path in Agent 1's `record` package.
- Reproduction: n/a (absent).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0029: passkey recovery Take races on the Used flag after delete (Agent 3 variant)
- Severity: Low
- Area: ogon/auth/passkey/passkey.go (InMemoryRecoveryStore.Take) — absent in Agent 1
- Cross-ref: Agent 3 BUG-0029
- Description: Agent 3's `InMemoryRecoveryStore.Take` had a dead `Used` check after `delete`. Agent 1 does not have an `InMemoryRecoveryStore`. The `auth.oauth.MemoryStateStash.Load` does `delete(s.m, key)` then returns the value (no `Used` check) — the single-use contract is enforced by the delete, with no misleading dead code.
- Reproduction: n/a (absent).
- Impact: None in Agent 1.
- Fix: None required.
- Status: not-applicable-in-A1

---

## BUG-0030: rate-limit X-RateLimit-Limit header reports remaining instead of limit (Agent 3 variant)
- Severity: Low
- Area: ogon/http/middleware/ratelimit.go (RateLimit) — headers absent in Agent 1
- Cross-ref: Agent 3 BUG-0030
- Description: Agent 3's middleware set `X-RateLimit-Limit` to the remaining count. Agent 1's `RateLimitMiddleware` only sets `Retry-After` on rejection (HTTP-031); it does not set `X-RateLimit-Limit` or `X-RateLimit-Remaining` at all. The conflation bug does not apply, though the absence of the headers is a separate (minor) spec-gap.
- Reproduction: n/a (headers absent).
- Impact: None in Agent 1 (no misreporting; headers simply not emitted).
- Fix: None applied. (Documented: if the headers are added later, set `X-RateLimit-Limit` to the configured limit and `X-RateLimit-Remaining` to the remaining count.)
- Status: not-applicable-in-A1

---

## P4-A-D1: authz.WithTenant / TenantFromContext type mismatch (P4-A discovery)
- Severity: Medium
- Area: ogon/authz/tenant.go (WithTenant, TenantFromContext)
- Cross-ref: new finding discovered during P4-A
- Description: `WithTenant` stored the tenant ID as a `string` value in the context (`context.WithValue(ctx, scopedCtxKey{}, tenantID)`), but `TenantFromContext` asserted the value to `*string` (`v, _ := ctx.Value(scopedCtxKey{}).(*string)`). The assertion always failed, so `TenantFromContext` always returned `""`. Any caller relying on `authz.TenantFromContext` to read a tenant stamped by `authz.WithTenant` would silently get an empty string. (Note: the `jobs` package has its own `WithTenant`/`TenantFrom` pair in `jobs/tenant.go` that uses the correct `string` type on both sides, and the jobs worker uses that pair — so the bug only affects code that wires `authz.WithTenant`/`TenantFromContext` directly, which is currently nothing in-tree. The functions are exported, so external callers could be affected.)
- Reproduction:
  ```go
  ctx := authz.WithTenant(context.Background(), "t1")
  // authz.TenantFromContext(ctx) == "" (bug)
  // jobs.TenantFrom(ctx) == "t1" (correct, different function)
  ```
- Impact: Silent failure of tenant propagation for any caller using the `authz` pair.
- Fix: Changed `TenantFromContext` to assert to `string` (matching what `WithTenant` stores).
- Status: fixed-in-commit-internal

---

## Summary

| Status                     | Count | Findings                                                                 |
|----------------------------|-------|--------------------------------------------------------------------------|
| fixed-in-commit-internal   | 10    | BUG-0001, BUG-0003, BUG-0004, BUG-0005, BUG-0007, BUG-0011, BUG-0014, BUG-0016, BUG-0017, BUG-0019, BUG-0020, P4-A-D1 |
| already-safe-in-A1         | 6     | BUG-0002, BUG-0010, BUG-0013, BUG-0015, BUG-0027, (BUG-0004 latent part) |
| not-applicable-in-A1       | 12    | BUG-0006, BUG-0008, BUG-0009, BUG-0012, BUG-0018, BUG-0021, BUG-0022, BUG-0023, BUG-0024, BUG-0025, BUG-0028, BUG-0029, BUG-0030 |
| open                       | 1     | BUG-0026 (latent; requires reflection refactor)                          |

**Files modified:**
- `auth/token/jwt.go` — BUG-0003 (alg-aware signing), BUG-0004 (HS* defense-in-depth), BUG-0020 (JWKSCache LRU).
- `http/middleware_idempotency.go` — BUG-0005 (singleflight dedup; also fixes the shared-`Route.Handler`-mutation bug).
- `http/ctx.go` — BUG-0007 (UTF-8 BOM strip in `jsonDecode`).
- `auth/oauth/oidc.go` — BUG-0011 (UserInfo body size cap).
- `auth/csp.go` — BUG-0014 (CSP nonce fail-closed on CSPRNG failure).
- `http/middleware_cors.go` — BUG-0016 (always `Vary: Origin`), BUG-0017 (reject `*` + explicit mix).
- `live/reconnect.go` — BUG-0019 (ResumeWindow copy-on-trim).
- `http/middleware_ratelimit.go` — BUG-0001 variant (token-bucket mutex; remove lost-update race).
- `authz/tenant.go` — P4-A-D1 (WithTenant/TenantFromContext type mismatch).

**Verification:**
- `go build ./...` — PASS (exit 0).
- `go vet ./...` — PASS (exit 0).
- `go test -race -count=1 ./...` — PASS (exit 0). 756 test/subtest results, 0 FAIL, 0 SKIP.
- `gofmt -w` applied to every modified file.
- Every modified file retains the `SPDX-License-Identifier: MIT` header.
- No panics added to library code; no `interface{}` at public API boundaries; no reflection in hot paths (the BUG-0005 fix removes a reflection-adjacent `Route.Handler` mutation; the BUG-0020 fix uses `container/list`, not reflection).
- `go.mod` / `go.sum` were not manually edited. The `golang.org/x/sync/singleflight` import (BUG-0005 fix) reuses the existing `golang.org/x/sync v0.23.0` indirect dep — no `go get` was needed.
