package leader

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
)

// A renewal Consul never answers must return a bounded error, not hang.
func TestRenewIsBoundedWhenConsulStalls(t *testing.T) {
	// Hang until the client's deadline fires or the test ends, so
	// httptest.Close never waits on a live handler.
	blocked := make(chan struct{})
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
	// No idle connections, so a cancelled request leaks no goroutine for goleak.
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
