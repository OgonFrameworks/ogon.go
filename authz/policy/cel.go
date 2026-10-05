// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// ABAC via CEL (SEC-024).
//
// CEL (Common Expression Language, Google) lets operators express
// attribute-based access policies like:
//
//   request.method == "GET" || subject.role == "admin"
//   resource.owner == subject.id
//
// We import the cel-go library lazily; if not configured, the gate is
// a no-op. The on-disk policy is signed (HMAC) so a compromised host
// can't smuggle a more permissive policy.

package policy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"sync"

	"github.com/OgonFrameworks/ogon.go/authz"
	"github.com/OgonFrameworks/ogon.go/diag"
)

// Engine is the abstract CEL evaluation contract. The real cel-go
// implementation lives in cel_impl.go (omitted from this phase to keep
// dep surface small); a no-op Engine is provided for tests.
type Engine interface {
	// Compile parses+compiles an expression. Returns a Value to Eval later.
	Compile(expr string) (Value, error)
	// Eval evaluates a previously-compiled value against bindings.
	Eval(ctx context.Context, v Value, bindings map[string]any) (any, error)
}

// Value is a compiled CEL program.
type Value interface{}

// NoopEngine returns ok for any expression and always evaluates true.
// Use only in tests; in production, wire a real CEL engine.
type NoopEngine struct{}

// Compile returns the input string as-is.
func (NoopEngine) Compile(expr string) (Value, error) { return expr, nil }

// Eval always returns true.
func (NoopEngine) Eval(_ context.Context, _ Value, _ map[string]any) (any, error) {
	return true, nil
}

// Gate is the policy entry point. It stores signed CEL expressions
// keyed by name. The signature is checked before evaluation so a
// runtime-patched policy can't bypass authorization.
type Gate struct {
	mu     sync.RWMutex
	engine Engine
	key    []byte // 32-byte HMAC key; zero to disable signing (tests only)
	rules  map[string]*rule
}

type rule struct {
	expr  string
	value Value
	sigOK bool // signature verified at load
}

// NewGate returns a Gate with the supplied engine and signing key.
func NewGate(engine Engine, key []byte) *Gate {
	return &Gate{
		engine: engine,
		key:    append([]byte(nil), key...),
		rules:  map[string]*rule{},
	}
}

// AddRule installs a named policy expression. If the gate has a key,
// expr must be the exact bytes signed (caller signs out-of-band).
func (g *Gate) AddRule(name, expr string, signature []byte) error {
	if g == nil {
		return errors.New("cel: nil gate")
	}
	v, err := g.engine.Compile(expr)
	if err != nil {
		return diag.Wrap(err, diag.Diag{Code: "OGON-SEC-024", Title: "cel: compile"})
	}
	sigOK := true
	if len(g.key) > 0 {
		mac := hmac.New(sha256.New, g.key)
		mac.Write([]byte(expr))
		want := mac.Sum(nil)
		sigOK = subtle.ConstantTimeCompare(want, signature) == 1
		if !sigOK {
			return diag.New("OGON-SEC-024", "cel: bad signature", "policy rejected")
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rules[name] = &rule{expr: expr, value: v, sigOK: sigOK}
	return nil
}

// Eval evaluates the named rule against bindings. Returns (result, err).
// Unknown rule → false + error (fail-closed).
func (g *Gate) Eval(ctx context.Context, name string, bindings map[string]any) (bool, error) {
	g.mu.RLock()
	r, ok := g.rules[name]
	g.mu.RUnlock()
	if !ok {
		return false, errors.New("cel: unknown rule")
	}
	if !r.sigOK {
		return false, errors.New("cel: rule signature not verified")
	}
	out, err := g.engine.Eval(ctx, r.value, bindings)
	if err != nil {
		return false, err
	}
	b, _ := out.(bool)
	return b, nil
}

// Handler wraps an http.Handler with a CEL gate. If the rule returns
// false, the request is rejected with 403. Bindings are populated by
// the bind function (which can read the subject from context).
func (g *Gate) Handler(name string, bind func(*http.Request) map[string]any, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g == nil {
			next.ServeHTTP(w, r) // no gate configured → allow (fail-open)
			return
		}
		ok, err := g.Eval(r.Context(), name, bind(r))
		if err != nil {
			http.Error(w, "policy error", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "forbidden by policy", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Ensure the authz.Subjects type is referenced (cross-package
// integration documented via the import).
var _ authz.Subjects

// ErrNoPolicy is returned when a rule is requested before any rule
// with that name has been added.
var ErrNoPolicy = errors.New("cel: no policy")
