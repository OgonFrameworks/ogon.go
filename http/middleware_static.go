// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Static + SPA middleware: serves files from a configured root, with
// path-traversal guards and SPA fallback (history-mode routing).
//
// HTTP-046: path traversal is REFUSED. No symlink escape. No hidden files.
// HTTP-047: SPA fallback serves index.html for unknown paths.

package http

import (
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// StaticConfig configures the static/SPA middleware.
type StaticConfig struct {
	// Root is the filesystem root. MUST be absolute. Symlinks outside Root
	// are REFUSED.
	Root string
	// IndexFile: default "index.html".
	IndexFile string
	// SPA: when true, missing paths fall back to IndexFile (history-mode).
	SPA bool
	// Prefix: when set, only paths under Prefix are served. E.g. "/assets".
	Prefix string
	// DenyHidden: refuse dotfiles. Default true.
	DenyHidden bool
	// StripPrefix: when true, strip Prefix from the path before serving.
	StripPrefix bool
}

// DefaultStaticConfig returns a production-safe config.
func DefaultStaticConfig(root string) StaticConfig {
	return StaticConfig{
		Root:       root,
		IndexFile:  "index.html",
		SPA:        false,
		DenyHidden: true,
	}
}

// StaticMiddleware returns the static file middleware.
func StaticMiddleware(cfg StaticConfig) Middleware {
	if cfg.IndexFile == "" {
		cfg.IndexFile = "index.html"
	}
	if cfg.Root == "" {
		panic("ogon/http: StaticMiddleware requires Root")
	}
	rootAbs, err := filepath.Abs(cfg.Root)
	if err != nil {
		panic("ogon/http: StaticMiddleware Root is not absolute: " + err.Error())
	}
	cfg.Root = rootAbs

	fs := http.Dir(cfg.Root)
	fileServer := http.FileServer(fs)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Apply prefix matching.
			reqPath := r.URL.Path
			if cfg.Prefix != "" {
				if !strings.HasPrefix(reqPath, cfg.Prefix) {
					next.ServeHTTP(w, r)
					return
				}
				if cfg.StripPrefix {
					reqPath = strings.TrimPrefix(reqPath, cfg.Prefix)
					if reqPath == "" {
						reqPath = "/"
					}
				}
			}

			// Path-traversal guard.
			clean := path.Clean("/" + reqPath)
			if strings.Contains(clean, "..") {
				_ = NotFoundProblem(r.URL.Path).Write(w, nil)
				return
			}
			// Hidden file guard.
			if cfg.DenyHidden {
				parts := strings.Split(strings.Trim(clean, "/"), "/")
				for _, p := range parts {
					if strings.HasPrefix(p, ".") {
						_ = NotFoundProblem(r.URL.Path).Write(w, nil)
						return
					}
				}
			}

			// Resolve on-disk path and check symlink escape.
			full := filepath.Join(cfg.Root, filepath.FromSlash(clean))
			real, err := filepath.EvalSymlinks(full)
			if err == nil {
				if !strings.HasPrefix(real, cfg.Root) {
					_ = NotFoundProblem(r.URL.Path).Write(w, nil)
					return
				}
			}

			// SPA fallback: missing file → serve index.html.
			if cfg.SPA {
				if _, statErr := os.Stat(full); statErr != nil {
					indexPath := filepath.Join(cfg.Root, cfg.IndexFile)
					http.ServeFile(w, r, indexPath)
					return
				}
			}

			// Strip prefix and dispatch to FileServer.
			if cfg.Prefix != "" && cfg.StripPrefix {
				r2 := new(http.Request)
				*r2 = *r
				r2.URL = &url.URL{Path: reqPath, RawQuery: r.URL.RawQuery}
				fileServer.ServeHTTP(w, r2)
				return
			}
			fileServer.ServeHTTP(w, r)
		})
	}
}

func init() {
	registerMiddleware("static", "static + SPA; path-traversal guard", 10)
}
