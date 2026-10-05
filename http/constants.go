// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Shared header constants used by multiple middlewares.

package http

// IdempotencyHeader is the canonical idempotency-key header (HTTP-035).
const IdempotencyHeader = "Idempotency-Key"

// APIVersionHeader selects the response API version per request (HTTP-043).
const APIVersionHeader = "Api-Version"

// DeprecationHeader marks a response as deprecated (RFC 9745 / HTTP-043).
const DeprecationHeader = "Deprecation"

// SunsetHeader announces the deprecation removal date (RFC 8594 / HTTP-043).
const SunsetHeader = "Sunset"

// LastEventIDHeader carries the last event id for SSE resume (HTTP-022).
const LastEventIDHeader = "Last-Event-ID"
