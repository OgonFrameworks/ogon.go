// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Upload hardening (SEC-048).
//
// Steps performed by SafeUpload:
//   1. Size cap — reject oversized uploads before reading.
//   2. MIME sniff — use net/http.DetectContentType on the first 512
//      bytes; ignore the declared Content-Type.
//   3. Sanitize — strip any leading path components; reject names with
//      ".." or absolute paths.
//   4. AV hook — call the optional AVScanner (ClamAV wire-up), reject
//      if infected.
//   5. Optional allowlist — reject if the detected MIME is not in
//      AllowedMimes (when set).

package auth

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// UploadConfig governs SafeUpload.
type UploadConfig struct {
	MaxBytes     int64    // hard cap; default 10MB
	AllowedMimes []string // when set, restrict allowed types
	// AllowedExtensions is checked alongside MIME for defense-in-depth.
	AllowedExtensions []string
}

// DefaultUploadConfig returns 10MB cap, no MIME allowlist (caller opt-in).
func DefaultUploadConfig() UploadConfig {
	return UploadConfig{MaxBytes: 10 * 1024 * 1024}
}

// AVScanner is the optional antivirus hook. Implementations return
// ErrInfected when the file is dirty.
type AVScanner interface {
	Scan(data []byte, filename string) error
}

// SafeUpload performs the upload-safety dance on one multipart file.
// Returns the cleaned filename, the sniffed MIME, and the bytes read
// (bounded by cfg.MaxBytes). The caller persists these; SafeUpload
// does not write to disk itself.
func SafeUpload(fh *multipart.FileHeader, cfg UploadConfig, av AVScanner) (filename, mime string, data []byte, err error) {
	if cfg.MaxBytes > 0 && fh.Size > cfg.MaxBytes {
		return "", "", nil, diag.New("OGON-SEC-048", "upload: too large",
			"size exceeds cap")
	}
	// sanitize filename: strip path components
	cleaned := filepath.Base(filepath.Clean(fh.Filename))
	if cleaned == "." || cleaned == ".." || strings.Contains(cleaned, "..") {
		return "", "", nil, diag.New("OGON-SEC-048", "upload: bad filename", fh.Filename)
	}
	// extension allowlist (when set)
	if len(cfg.AllowedExtensions) > 0 {
		ext := strings.ToLower(filepath.Ext(cleaned))
		ok := false
		for _, w := range cfg.AllowedExtensions {
			if ext == strings.ToLower(w) {
				ok = true
				break
			}
		}
		if !ok {
			return "", "", nil, diag.New("OGON-SEC-048", "upload: extension not allowed", ext)
		}
	}
	// open and read (capped)
	f, err := fh.Open()
	if err != nil {
		return "", "", nil, err
	}
	defer f.Close()
	limited := io.LimitReader(f, cfg.MaxBytes+1)
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, limited); err != nil {
		return "", "", nil, err
	}
	if int64(buf.Len()) > cfg.MaxBytes {
		return "", "", nil, diag.New("OGON-SEC-048", "upload: too large", "size exceeded cap during read")
	}
	// MIME sniff — never trust the declared Content-Type
	sniff := buf.Bytes()
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	detected := http.DetectContentType(sniff)
	if len(cfg.AllowedMimes) > 0 {
		ok := false
		for _, m := range cfg.AllowedMimes {
			if strings.HasPrefix(detected, m) {
				ok = true
				break
			}
		}
		if !ok {
			return "", "", nil, diag.New("OGON-SEC-048", "upload: mime not allowed", detected)
		}
	}
	// AV scan (if configured)
	if av != nil {
		if err := av.Scan(buf.Bytes(), cleaned); err != nil {
			return "", "", nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-048", Title: "upload: AV rejected"})
		}
	}
	return cleaned, detected, buf.Bytes(), nil
}

// ErrInfected is the sentinel returned by AVScanner implementations.
var ErrInfected = errors.New("upload: file infected")
