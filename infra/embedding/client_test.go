package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRetriesOn503ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer srv.Close()

	c := newHTTPClient(defaultTimeout, nil)
	c.backoff = time.Millisecond

	var out map[string]string
	if err := c.doJSON(context.Background(), http.MethodPost, srv.URL, nil, map[string]string{}, &out); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if out["ok"] != "yes" {
		t.Errorf("out = %v", out)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3 (2 retries)", got)
	}
}

func TestClientNonRetryableError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "bad request body", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newHTTPClient(defaultTimeout, nil)
	c.backoff = time.Millisecond

	var out map[string]string
	err := c.doJSON(context.Background(), http.MethodPost, srv.URL, nil, map[string]string{}, &out)

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("want APIError 400, got %v", err)
	}
	if apiErr.Body == "" {
		t.Error("APIError body should carry the response snippet")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (400 not retried)", got)
	}
}
