// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// TS escape hatch `Ogon.Call()` typed bridge + shared TS types gen
// (UI-046/037). The bridge lets advanced components call server-side
// Go handlers from TS without losing type safety.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// TSBridge is the per-session typed RPC bridge. Generated Go code
// registers typed handlers under a string name; the client runtime
// invokes `Ogon.Call("HandlerName", args)` over the live transport.
type TSBridge struct {
	mu       sync.RWMutex
	handlers map[string]TSHandler
}

// TSHandler is a single registered escape-hatch handler. The
// payload is a map[string]any decoded from the JSON the client
// sent; the return value is JSON-encoded back.
type TSHandler func(ctx context.Context, args map[string]any) (any, error)

// NewTSBridge constructs an empty bridge.
func NewTSBridge() *TSBridge { return &TSBridge{handlers: map[string]TSHandler{}} }

// Register adds a typed escape-hatch handler.
func (b *TSBridge) Register(name string, h TSHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[name] = h
}

// Call invokes a handler by name (UI-046). Returns ErrBridgeUnknown
// when no handler matches.
func (b *TSBridge) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	b.mu.RLock()
	h, ok := b.handlers[name]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrBridgeUnknown, name)
	}
	return h(ctx, args)
}

// Names returns the registered handler names (for the generated TS
// types manifest).
func (b *TSBridge) Names() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.handlers))
	for n := range b.handlers {
		out = append(out, n)
	}
	return out
}

// ErrBridgeUnknown is returned by Call when the handler name is not
// registered.
var ErrBridgeUnknown = errors.New("ogon/ui: unknown bridge handler")

// TSTypeDecl is a single shared type declaration emitted into the
// generated `ogon.d.ts` file (UI-037). The compiler produces these
// from the `<go>` block Props/State types so frontend authors get
// typed props without manual sync.
type TSTypeDecl struct {
	Name   string
	GoType string
	TSBody string
}

// EmitDTS renders a sequence of TSTypeDecls as a single `.d.ts`
// fragment. The fragment is concatenated into `ogon.d.ts` at build
// time so editor autocomplete and the TS compiler can resolve
// `Ogon.Call()` argument types.
func EmitDTS(decls []TSTypeDecl) string {
	out := ""
	for _, d := range decls {
		out += "export interface " + d.Name + " " + d.TSBody + "\n"
	}
	return out
}

// GoToTS maps a Go type name to its TS equivalent for the shared
// types manifest (UI-037). The mapping is conservative; full type
// translation is left to a future codegen pass.
func GoToTS(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "int", "int64", "int32", "uint", "uint64", "float64", "float32":
		return "number"
	case "bool":
		return "boolean"
	default:
		return "any"
	}
}
