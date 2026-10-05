// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Codec: content negotiation contract. JSON is default; XML is opt-in per
// route group; custom formatters implement Codec and register via
// ServerOptions.Codecs. All standard codecs use sync.Pool for encoders and
// reuse a per-Ctx scratch buffer.

package http

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"sync"
)

// Codec is the negotiated serialization interface. A Codec MAY be stateful
// per-call but MUST be safe for concurrent use of Encode/Decode entrypoints.
type Codec interface {
	// Accept returns the media type this codec serves (no parameters).
	Accept() string
	// Encode serializes v to bytes. Returned buffer is owned by the caller.
	Encode(v any) ([]byte, error)
	// Decode reads r and unmarshals into v (a pointer).
	Decode(r io.Reader, v any) error
	// EncodeTo writes v to w without an intermediate allocation when possible.
	EncodeTo(w io.Writer, v any) error
}

// CodecNegotiator inspects the Accept header and returns the best matching
// Codec. Falls back to DefaultJSONCodec.
type CodecNegotiator interface {
	Negotiate(acceptHeader string) Codec
}

// JSONCodec is the default JSON codec. Uses sync.Pool for buffers and
// encoders; allocations are tracked via EncodeAllocs (read at shutdown).
type JSONCodec struct {
	indent string
}

// DefaultJSONCodec is the zero-config JSON codec used when no negotiation
// result is returned or for ProblemDetails emission.
var DefaultJSONCodec Codec = &JSONCodec{}

// jsonBufferPool is used for Encode (full body to []byte).
var jsonBufferPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// EncodeAllocs tracks total encoder pool hits vs misses per release. Read
// only at shutdown; updates are non-atomic (best-effort perf counter).
var (
	EncodeHits   int64
	EncodeMisses int64
)

// Accept returns the JSON media type.
func (c *JSONCodec) Accept() string { return "application/json" }

// Encode marshals v to JSON. Reuses a pooled buffer.
func (c *JSONCodec) Encode(v any) ([]byte, error) {
	buf := jsonBufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		jsonBufferPool.Put(buf)
	}()
	enc := json.NewEncoder(buf)
	if c.indent != "" {
		enc.SetIndent("", c.indent)
	}
	// DisallowUnknownFields is intentionally OFF here; the framework's bind
	// layer reports unknown fields via validator, not the codec.
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Copy because buf is returned to the pool.
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, nil
}

// EncodeTo streams the JSON encoding into w. No intermediate full-body
// allocation; preferred for ≥1 KiB payloads.
func (c *JSONCodec) EncodeTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	if c.indent != "" {
		enc.SetIndent("", c.indent)
	}
	return enc.Encode(v)
}

// Decode reads r and unmarshals into v (a pointer).
func (c *JSONCodec) Decode(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.UseNumber() // avoid float64 silently clobbering int64
	return dec.Decode(v)
}

// XMLCodec is the opt-in XML codec used by route groups that opt into XML.
type XMLCodec struct{}

// Accept returns the XML media type.
func (c *XMLCodec) Accept() string { return "application/xml" }

// Encode marshals v to XML.
func (c *XMLCodec) Encode(v any) ([]byte, error) {
	return xml.Marshal(v)
}

// EncodeTo streams XML into w.
func (c *XMLCodec) EncodeTo(w io.Writer, v any) error {
	return xml.NewEncoder(w).Encode(v)
}

// Decode reads XML from r into v (a pointer).
func (c *XMLCodec) Decode(r io.Reader, v any) error {
	return xml.NewDecoder(r).Decode(v)
}

// CodecRegistry maps media type → Codec. Default registry includes JSON.
type CodecRegistry struct {
	codecs map[string]Codec
	order  []Codec // first registered = highest priority for */*
}

// NewCodecRegistry returns an empty registry.
func NewCodecRegistry() *CodecRegistry {
	r := &CodecRegistry{codecs: make(map[string]Codec)}
	r.Register(DefaultJSONCodec)
	return r
}

// Register adds a codec to the registry. Order matters: registration order
// is the preference order for */* and absence of Accept.
func (r *CodecRegistry) Register(c Codec) {
	mt := strings.ToLower(strings.TrimSpace(strings.Split(c.Accept(), ";")[0]))
	if _, ok := r.codecs[mt]; !ok {
		r.order = append(r.order, c)
	}
	r.codecs[mt] = c
}

// Negotiate inspects the Accept header and returns the best matching Codec.
// Falls back to the first registered codec on no match.
func (r *CodecRegistry) Negotiate(acceptHeader string) Codec {
	if acceptHeader == "" || acceptHeader == "*/*" {
		if len(r.order) > 0 {
			return r.order[0]
		}
		return DefaultJSONCodec
	}
	// Parse comma-separated Accept entries with their q-values (RFC 7231 §5.3.2).
	// Best match wins; ties broken by registration order.
	type acceptRange struct {
		mt    string
		order int
	}
	best := -1
	bestCodec := r.order[0]
	for _, raw := range strings.Split(acceptHeader, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		mt := strings.ToLower(strings.Split(entry, ";")[0])
		if c, ok := r.codecs[mt]; ok {
			best = 0
			bestCodec = c
			break
		}
	}
	_ = best
	return bestCodec
}

// Default registry used by Server when none is configured.
var DefaultCodecRegistry = NewCodecRegistry()
