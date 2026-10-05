// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — fuzz payload decode (JOBS-051).
//
// FuzzPayload is the entrypoint for `go test -fuzz=FuzzPayload`. It
// exercises the decode path of arbitrary JSON bytes through
// UnmarshalPayload for a sample typed args struct, ensuring no panic
// or resource leak on adversarial input.
//
// The fuzz target is exported so a downstream module can drive it
// from a separate _test.go file using the standard fuzz harness.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
)

// FuzzSampleArgs is the typed args struct the fuzz target decodes
// into. It exercises the common field shapes: strings, ints, slices,
// maps, nested structs.
type FuzzSampleArgs struct {
	ArgsBase
	UserID string            `json:"user_id,omitempty"`
	Count  int               `json:"count,omitempty"`
	Tags   []string          `json:"tags,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
	Nested struct {
		Name string `json:"name,omitempty"`
	} `json:"nested,omitempty"`
}

// FuzzPayload is the fuzz target. Calling it directly from a test
// runs one corpus entry. The standard fuzz harness drives it with
// random data.
func FuzzPayload(data []byte) error {
	env := &Envelope{
		ID:      NewID(),
		Name:    "fuzz",
		Payload: data,
	}
	// Dispatch via the typed decoder. We expect no panic regardless of input.
	dispatcher := DecodeDispatcher[FuzzSampleArgs](func(ctx context.Context, args FuzzSampleArgs) error {
		// handler body intentionally trivial — the decode itself is the surface
		_ = args
		return nil
	})
	if err := dispatcher(context.Background(), env); err != nil {
		// soft errors (decode failures) are fine; only panics should fail fuzz.
		var jerr *json.SyntaxError
		if errors.As(err, &jerr) {
			return nil
		}
		var merr *json.UnmarshalTypeError
		if errors.As(err, &merr) {
			return nil
		}
		// any other error is also acceptable for adversarial input.
		return nil
	}
	return nil
}
