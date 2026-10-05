// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzPayloadDecode is the go-fuzz entry target (JOBS-051).
// Run with: `go test ./jobs/ -fuzz=FuzzPayloadDecode`
//
// The target exercises the decode path with arbitrary bytes; it MUST
// not panic regardless of input. Soft errors (decode failures) are
// acceptable and ignored.
//
// (P14 bug-bounty: extended seed corpus with adversarial shapes —
// very deep nesting, huge repeated values, embedded NULs, mixed
// numeric/string types, surrogate-pair unicode, etc.)
func FuzzPayloadDecode(f *testing.F) {
	corpus := [][]byte{
		nil,
		{},
		[]byte("not json"),
		[]byte(`{"user_id":"abc"}`),
		[]byte(`{"count": 42, "tags": ["a","b"], "meta": {"k":"v"}}`),
		[]byte(`{"nested": {"name": "deep"}}`),
		[]byte(`{`),
		[]byte(`{"tags": [`),
		[]byte(`{"count": "str"}`),
		[]byte(`{"nested": {"name": "x"}`),
		// P14 seed extensions:
		[]byte(`null`),           // JSON null
		[]byte(`[]`),             // top-level array
		[]byte(`true`),           // top-level bool
		[]byte(`42`),             // top-level number
		[]byte(`"`),              // dangling string
		[]byte(`"\u0000"`),       // NUL escape
		[]byte(`"\uD83D\uDE00"`), // surrogate pair (emoji)
		[]byte(`{"count":9999999999999999999999}`),                               // huge int
		[]byte(`{"count":1e308}`),                                                // huge float
		[]byte(`{"tags":` + strings.Repeat(`"x",`, 100) + `"x"}`),                // big array
		[]byte(strings.Repeat(`{"nested":`, 20) + `1` + strings.Repeat(`}`, 20)), // deep nesting
		[]byte(`{"meta":{"a":"b","c":"d","e":"f","g":"h"}}`),                     // map with extras
		[]byte(`{"user_id":null,"count":null,"tags":null}`),                      // null values
		[]byte("\xff\xfe\x00\x01garbage"),                                        // binary garbage
	}
	for _, c := range corpus {
		f.Add(c)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// The target simply must not panic.
		_ = FuzzPayload(data)
	})
}

func TestFuzzPayloadCorpusCases(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte("not json"),
		[]byte(`{"user_id":"x"}`),
		[]byte(`{"count": 42, "tags": ["a","b"], "meta": {"k":"v"}}`),
		[]byte(`{"nested": {"name": "deep"}}`),
		[]byte(`{`),
		[]byte(`{"tags": [`),
		[]byte(`{"count": "str"}`),
		[]byte(`{"nested": {"name": "x"}`),
	}
	for _, c := range cases {
		if err := FuzzPayload(c); err != nil {
			t.Errorf("FuzzPayload(%q) returned err %v; want nil", string(c), err)
		}
	}
}

func TestFuzzSampleArgsRoundTrip(t *testing.T) {
	a := FuzzSampleArgs{UserID: "u", Count: 3, Tags: []string{"x"}}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got FuzzSampleArgs
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.UserID != "u" || got.Count != 3 || len(got.Tags) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}
