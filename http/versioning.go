// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Versioning: path prefix (/v1) and/or Api-Version header. Deprecated
// versions emit Deprecation + Sunset headers (HTTP-043).

package http

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// VersionRule declares a supported API version and its deprecation status.
type VersionRule struct {
	// Version is the bare version string, e.g. "1", "2".
	Version string
	// Status: "active" (default), "deprecated", "sunset".
	Status string
	// SunsetDate: when the version will be removed (RFC 8594). Zero value
	// means no removal date set.
	SunsetDate time.Time
	// DeprecationDate: when the version was marked deprecated (RFC 9745).
	DeprecationDate time.Time
}

// VersioningConfig configures the versioning middleware.
type VersioningConfig struct {
	// PrefixVersions: when true, the path prefix /v1, /v2 is parsed as a
	// version selector and stripped before routing. Default true.
	PrefixVersions bool
	// HeaderVersion: when true, the Api-Version header selects the response
	// version. Default true.
	HeaderVersion bool
	// Versions: declared versions in semver order (newest last). Required.
	Versions []VersionRule
}

// VersioningMiddleware inspects the request, selects the matching version,
// and emits Deprecation/Sunset headers when applicable.
//
// HTTP-043: deprecated versions return 200 with headers; sunset versions
// return 410 Gone with a ProblemDetails explaining removal.
func VersioningMiddleware(cfg VersioningConfig) Middleware {
	// Build a lookup table.
	rules := make(map[string]VersionRule, len(cfg.Versions))
	newest := ""
	for _, v := range cfg.Versions {
		rules[v.Version] = v
		if v.Version > newest {
			newest = v.Version
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Parse the path prefix.
			requestedVersion := ""
			path := r.URL.Path
			if cfg.PrefixVersions {
				if strings.HasPrefix(path, "/v") {
					rest := path[2:]
					// Find the next slash.
					slash := strings.IndexByte(rest, '/')
					if slash > 0 {
						requestedVersion = rest[:slash]
						path = "/" + rest[slash+1:]
					} else if slash < 0 {
						// No trailing slash; treat entire rest as version.
						requestedVersion = rest
						path = "/"
					}
					// Rewrite the request path so the router sees the bare path.
					r2 := new(http.Request)
					*r2 = *r
					r2.URL = &url.URL{Path: path, RawQuery: r.URL.RawQuery, Fragment: r.URL.Fragment}
					r = r2
				}
			}
			if cfg.HeaderVersion && requestedVersion == "" {
				if v := r.Header.Get(APIVersionHeader); v != "" {
					requestedVersion = v
				}
			}

			// If no version requested, use newest by default.
			if requestedVersion == "" {
				requestedVersion = newest
			}

			rule, ok := rules[requestedVersion]
			if !ok {
				// Unknown version → 400.
				p := NewProblem(http.StatusBadRequest,
					"Unsupported API version",
					"version "+requestedVersion+" is not supported")
				_ = p.Write(w, nil)
				return
			}

			// Emit Deprecation/Sunset headers if applicable.
			if rule.Status == "deprecated" {
				val := "true"
				if !rule.DeprecationDate.IsZero() {
					val = rule.DeprecationDate.UTC().Format(time.RFC1123)
				}
				w.Header().Set(DeprecationHeader, val)
				if !rule.SunsetDate.IsZero() {
					w.Header().Set(SunsetHeader, rule.SunsetDate.UTC().Format(time.RFC1123))
				}
			} else if rule.Status == "sunset" {
				p := NewProblem(http.StatusGone,
					"API version removed",
					"version "+requestedVersion+" was sunset")
				if !rule.SunsetDate.IsZero() {
					p.SetExtension("sunset", rule.SunsetDate.UTC().Format(time.RFC1123))
				}
				_ = p.Write(w, nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func init() {
	registerMiddleware("versioning", "/v1 prefix + Api-Version header; Deprecation/Sunset", 13)
}
