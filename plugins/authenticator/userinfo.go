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

// fetchUserInfo calls the IdP userinfo endpoint and returns the decoded JSON body.
func fetchUserInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	body, err := doUserInfoRequest(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}
	return data, nil
}

// fetchUserInfoRaw is like fetchUserInfo but also returns the raw response body
// for passthrough. parsed is nil if the body is not valid JSON.
func fetchUserInfoRaw(ctx context.Context, accessToken string) (raw []byte, parsed map[string]any, err error) {
	raw, err = doUserInfoRequest(ctx, accessToken)
	if err != nil {
		return raw, nil, err
	}
	parsed = map[string]any{}
	if json.Unmarshal(raw, &parsed) != nil {
		return raw, nil, nil
	}
	return raw, parsed, nil
}

// doUserInfoRequest performs the IdP /userinfo HTTP call with trace propagation.
// Returns the body on HTTP 200; otherwise returns body (possibly nil) and a non-nil error.
func doUserInfoRequest(ctx context.Context, accessToken string) ([]byte, error) {
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
		return body, fmt.Errorf("userinfo status %d", resp.StatusCode)
	}
	return body, nil
}
