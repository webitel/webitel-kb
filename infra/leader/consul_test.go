package leader

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
)

// A renewal Consul never answers must return a bounded error, not hang.
func TestRenewIsBoundedWhenConsulStalls(t *testing.T) {
	blocked := make(chan struct{})

	// Hang until the client's deadline fires or the test ends, so
	// httptest.Close never waits on a live handler.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-blocked:
		}
	}))
	defer srv.Close()
	defer close(blocked)

	cfg := api.DefaultConfig()
	cfg.Address = srv.URL
	// No idle connections, so a canceled request leaks no goroutine for goleak.
	cfg.Transport.DisableKeepAlives = true
	defer cfg.Transport.CloseIdleConnections()

	client, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}

	adapter := consulClient{client}

	done := make(chan struct{})
	errc := make(chan error, 1)

	go func() { errc <- adapter.Renew("200ms", "sess", done) }()

	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("a stalled renewal returned nil; expected a bounded deadline error")
		}
	case <-time.After(2 * time.Second):
		close(done)
		t.Fatal("Renew did not return; a stalled request was not bounded")
	}
}

// Renew returns promptly when the term ends.
func TestRenewStopsWhenDoneCloses(t *testing.T) {
	client, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	errc := make(chan error, 1)

	go func() { errc <- consulClient{client}.Renew("10s", "sess", done) }()

	close(done)

	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Renew returned %v on a clean stop; want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Renew did not return after done closed")
	}
}

// A renewal that finds the session gone must end the term. Consul answers a
// dead session with 404, which the client reports as a nil entry and no error.
func TestRenewEndsWhenTheSessionIsGone(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
	}{
		{name: "session not found", code: http.StatusNotFound, body: ""},
		{name: "empty renewal list", code: http.StatusOK, body: "null"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			cfg := api.DefaultConfig()
			cfg.Address = srv.URL
			cfg.Transport.DisableKeepAlives = true

			defer cfg.Transport.CloseIdleConnections()

			client, err := api.NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}

			done := make(chan struct{})
			defer close(done)

			errc := make(chan error, 1)

			go func() { errc <- consulClient{client}.Renew("200ms", "sess", done) }()

			select {
			case err := <-errc:
				if !errors.Is(err, api.ErrSessionExpired) {
					t.Fatalf("Renew returned %v; want %v", err, api.ErrSessionExpired)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Renew did not return; a lost session passed for a success")
			}
		})
	}
}
