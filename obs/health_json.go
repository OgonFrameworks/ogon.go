// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon health --json`: CLI-side serializer for /healthz. Implements OBS-038.
// The CLI calls the same HealthService mounted on the server (via the
// internal http client) and emits the response in the stable JSON
// envelope. The CLI's human renderer lives in cli/output.go.

package obs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HealthJSON issues a GET against the supplied /healthz URL and returns
// the decoded response. Used by `ogon health --json` so the CLI and the
// HTTP server share one rendering contract. (OBS-038)
//
// auth is an optional bearer token (OBS-022). Pass "" to skip auth.
func HealthJSON(ctx context.Context, url, auth string) (*HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("obs: build request: %w", err)
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("obs: probe: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("obs: read probe: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("obs: probe returned %d: %s", resp.StatusCode, string(body))
	}
	var hr HealthResponse
	if err := json.Unmarshal(body, &hr); err != nil {
		return nil, fmt.Errorf("obs: decode probe: %w", err)
	}
	return &hr, nil
}

// RenderHealthJSON serializes hr to a stable JSON blob for the CLI.
// (OBS-038)
func RenderHealthJSON(hr *HealthResponse) ([]byte, error) {
	return json.MarshalIndent(hr, "", "  ")
}
