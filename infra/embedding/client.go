package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	defaultTimeout = 30 * time.Second
	defaultRetries = 2
	// maxErrorBody bounds how much of a non-2xx response body is retained.
	maxErrorBody = 2 << 10
)

// APIError describes a non-2xx response from a provider.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("embedding: provider returned status %d: %s", e.StatusCode, e.Body)
}

// httpClient is a small JSON-over-HTTP helper shared by the providers. It adds
// a timeout, OpenTelemetry tracing, and a bounded retry with backoff on 429/5xx.
type httpClient struct {
	hc      *http.Client
	retries int
	backoff time.Duration
}

func newHTTPClient(timeout time.Duration, hc *http.Client) *httpClient {
	if hc == nil {
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		hc = &http.Client{
			Timeout:   timeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
	}
	return &httpClient{hc: hc, retries: defaultRetries, backoff: 200 * time.Millisecond}
}

// doJSON marshals reqBody, performs the request with retries, and decodes a 2xx
// response into respOut. A non-2xx response (after retries) yields an *APIError.
func (c *httpClient) doJSON(ctx context.Context, method, url string, headers map[string]string, reqBody, respOut any) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("embedding: marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, c.backoff<<(attempt-1)); err != nil {
				return err
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("embedding: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("embedding: request failed: %w", err)
			continue
		}

		body, apiErr, retry := readResponse(resp)
		if retry {
			lastErr = apiErr
			continue
		}
		if apiErr != nil {
			return apiErr
		}

		if err := json.Unmarshal(body, respOut); err != nil {
			return fmt.Errorf("embedding: decode response: %w", err)
		}
		return nil
	}

	return lastErr
}

// readResponse reads the body and classifies the status: it returns the body on
// 2xx, an *APIError otherwise, and reports whether the status is retryable.
func readResponse(resp *http.Response) (body []byte, apiErr error, retry bool) {
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("embedding: read response: %w", err), false
		}
		return b, nil, false
	}

	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	err := &APIError{StatusCode: resp.StatusCode, Body: string(snippet)}
	retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return nil, err, retryable
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
