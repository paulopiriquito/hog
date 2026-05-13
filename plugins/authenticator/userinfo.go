package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/paulopiriquito/hog/pkg/headers"
)

// fetchUserInfo calls the IdP's discovered userinfo endpoint with the given
// access token and returns the decoded JSON body as a map. It propagates trace
// headers from the incoming context for distributed tracing.
func fetchUserInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	if oidcConfig == nil {
		return nil, fmt.Errorf("oidc not initialized")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, oidcConfig.UserinfoEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	traceFormat := os.Getenv("TRACE_FORMAT")
	if traceFormat == "" {
		traceFormat = headers.TraceFormatOTEL
	}
	headers.InjectTraceHeaders(ctx, req, traceFormat)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read userinfo body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo status %d: %s", resp.StatusCode, string(body))
	}

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}
	return data, nil
}

// fetchUserInfoRaw is a thin wrapper that also returns the raw body, used by
// handleUserInfo when it needs to pass the IdP response through unchanged.
// If the body is not valid JSON, parsed is nil and err is nil — the caller
// is expected to fall back to raw passthrough in that case.
func fetchUserInfoRaw(ctx context.Context, accessToken string) (rawBody []byte, parsed map[string]any, err error) {
	if oidcConfig == nil {
		return nil, nil, fmt.Errorf("oidc not initialized")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, oidcConfig.UserinfoEndpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	traceFormat := os.Getenv("TRACE_FORMAT")
	if traceFormat == "" {
		traceFormat = headers.TraceFormatOTEL
	}
	headers.InjectTraceHeaders(ctx, req, traceFormat)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("userinfo request: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read userinfo body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return rawBody, nil, fmt.Errorf("userinfo status %d", resp.StatusCode)
	}

	parsed = map[string]any{}
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		// Body is OK but not JSON — return raw body, no parsed map, no error.
		return rawBody, nil, nil
	}
	return rawBody, parsed, nil
}
