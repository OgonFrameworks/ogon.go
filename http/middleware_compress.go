// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Compression middleware: gzip + zstd. Uses klauspost/compress for both.
// Negotiates per Accept-Encoding. Threshold: only compress responses ≥ 1 KiB
// (smaller responses cost more in overhead than they save).

package http

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// CompressConfig configures the compress middleware.
type CompressConfig struct {
	// MinSize: responses smaller than this are passed through uncompressed.
	// Default 1024.
	MinSize int
	// GzipLevel: 1-9; default 5 (good balance).
	GzipLevel int
	// EnableZstd: when true and the client accepts zstd, use it (preferred).
	EnableZstd bool
}

// DefaultCompressConfig returns a production-safe config.
func DefaultCompressConfig() CompressConfig {
	return CompressConfig{
		MinSize:    1024,
		GzipLevel:  5,
		EnableZstd: true,
	}
}

// gzipPool reuses gzip writers to avoid alloc-per-request. The compression
// level is fixed at DefaultCompressConfig.GzipLevel; per-request level
// changes require a separate pool.
var gzipPool = sync.Pool{
	New: func() any {
		z, _ := gzip.NewWriterLevel(io.Discard, DefaultCompressConfig().GzipLevel)
		return z
	},
}

// zstdPool reuses zstd encoders.
var zstdPool = sync.Pool{
	New: func() any {
		enc, _ := zstd.NewWriter(io.Discard)
		return enc
	},
}

// CompressMiddleware negotiates gzip/zstd per Accept-Encoding.
//
// Hot path: pool reuse for encoders and intermediate buffers. The wrapped
// ResponseWriter captures bytes until the threshold is exceeded; once the
// threshold is crossed, the response is committed to the encoder and the
// remainder streams through.
func CompressMiddleware() Middleware {
	return CompressMiddlewareWith(DefaultCompressConfig())
}

// CompressMiddlewareWith returns a middleware using cfg.
func CompressMiddlewareWith(cfg CompressConfig) Middleware {
	if cfg.MinSize <= 0 {
		cfg.MinSize = 1024
	}
	if cfg.GzipLevel <= 0 || cfg.GzipLevel > 9 {
		cfg.GzipLevel = 5
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip for HEAD/OPTIONS.
			if r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			// Skip SSE/WS upgrade paths.
			if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
				next.ServeHTTP(w, r)
				return
			}
			enc := negotiateEncoding(r.Header.Get("Accept-Encoding"), cfg.EnableZstd)
			if enc == "" {
				next.ServeHTTP(w, r)
				return
			}

			rec := &compressWriter{
				ResponseWriter: w,
				cfg:            cfg,
				encoding:       enc,
				buf:            new(bytes.Buffer),
			}
			defer rec.Flush()
			next.ServeHTTP(rec, r)
		})
	}
}

// compressWriter buffers the response up to MinSize, then switches to the
// negotiated encoder.
type compressWriter struct {
	http.ResponseWriter
	cfg           CompressConfig
	encoding      string
	buf           *bytes.Buffer
	encoder       io.WriteCloser
	headerWritten bool
}

// WriteHeader triggers the threshold check on the first byte.
func (c *compressWriter) WriteHeader(code int) {
	if !c.headerWritten {
		c.headerWritten = true
		// Set encoding headers on the underlying writer.
		c.ResponseWriter.Header().Set("Content-Encoding", c.encoding)
		c.ResponseWriter.Header().Set("Vary", "Accept-Encoding")
		c.ResponseWriter.Header().Del("Content-Length")
	}
	c.ResponseWriter.WriteHeader(code)
}

// Write buffers bytes up to MinSize; then commits to encoder.
func (c *compressWriter) Write(p []byte) (int, error) {
	if c.encoder != nil {
		return c.encoder.Write(p)
	}
	c.buf.Write(p)
	if c.buf.Len() >= c.cfg.MinSize {
		// Commit to encoding.
		if !c.headerWritten {
			c.WriteHeader(http.StatusOK)
		}
		c.encoder = c.acquireEncoder(c.ResponseWriter)
		_, _ = c.encoder.Write(c.buf.Bytes())
		c.buf.Reset()
	}
	return len(p), nil
}

// Flush flushes the encoder and underlying writer.
func (c *compressWriter) Flush() {
	if c.encoder == nil {
		// Below threshold: write the buffered bytes verbatim.
		if c.buf.Len() > 0 {
			if !c.headerWritten {
				c.ResponseWriter.WriteHeader(http.StatusOK)
			}
			// Remove Content-Encoding header we set prematurely.
			c.ResponseWriter.Header().Del("Content-Encoding")
			_, _ = c.ResponseWriter.Write(c.buf.Bytes())
		}
		return
	}
	_ = c.encoder.Close()
	c.releaseEncoder()
}

func (c *compressWriter) acquireEncoder(w io.Writer) io.WriteCloser {
	switch c.encoding {
	case "gzip":
		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(w)
		return &closableGzip{gz: gz}
	case "zstd":
		zw := zstdPool.Get().(*zstd.Encoder)
		zw.Reset(w)
		return &closableZstd{zw: zw}
	}
	return nopCloser{w}
}

func (c *compressWriter) releaseEncoder() {
	// The encoder is returned to the pool in Close().
}

// closableGzip wraps gzip.Writer to return it to the pool on Close.
type closableGzip struct{ gz *gzip.Writer }

func (c *closableGzip) Write(p []byte) (int, error) { return c.gz.Write(p) }
func (c *closableGzip) Close() error {
	c.gz.Close()
	gzipPool.Put(c.gz)
	return nil
}

// closableZstd wraps zstd.Encoder to return it to the pool on Close.
type closableZstd struct{ zw *zstd.Encoder }

func (c *closableZstd) Write(p []byte) (int, error) { return c.zw.Write(p) }
func (c *closableZstd) Close() error {
	c.zw.Close()
	zstdPool.Put(c.zw)
	return nil
}

// nopCloser is the fallback when the negotiated encoding is unsupported.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// negotiateEncoding returns "zstd", "gzip", or "" based on the
// Accept-Encoding header and the EnableZstd flag. Honors q-values (RFC 7231).
func negotiateEncoding(accept string, enableZstd bool) string {
	if accept == "" {
		return ""
	}
	best := ""
	bestQ := 0.0
	for _, raw := range strings.Split(accept, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		name := entry
		q := 1.0
		if i := strings.IndexByte(entry, ';'); i >= 0 {
			name = strings.TrimSpace(entry[:i])
			rest := strings.TrimSpace(entry[i+1:])
			if strings.HasPrefix(rest, "q=") {
				if fv, err := strconv.ParseFloat(rest[2:], 64); err == nil {
					q = fv
				}
			}
		}
		if name == "zstd" && enableZstd && q > bestQ {
			best, bestQ = "zstd", q
		} else if name == "gzip" && q > bestQ {
			best, bestQ = "gzip", q
		}
	}
	return best
}

func init() {
	registerMiddleware("compress", "gzip + zstd; min-size threshold; pool reuse", 9)
}
