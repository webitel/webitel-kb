package reliable

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// closedPort returns an address that was just listening and no longer is, so a
// dial to it is refused rather than hanging.
func closedPort(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	addr := l.Addr().String()
	_ = l.Close()

	return addr
}

func TestNewPublisherDoesNotDial(t *testing.T) {
	// A publisher for an unreachable broker must construct and close cleanly:
	// the dial is lazy by contract.
	p := NewPublisher("amqp://guest:guest@" + closedPort(t) + "/")

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPublishUnreachableBroker(t *testing.T) {
	p := NewPublisher("amqp://guest:guest@" + closedPort(t) + "/")
	defer p.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err := p.Publish(ctx, "x", "k", Message{Body: []byte("{}")})
	if err == nil {
		t.Fatal("Publish succeeded against a dead broker")
	}
}

func TestPublishAfterClose(t *testing.T) {
	p := NewPublisher("amqp://guest:guest@" + closedPort(t) + "/")

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := p.Publish(t.Context(), "x", "k", Message{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish error = %v, want ErrClosed", err)
	}

	if err := p.Declare(t.Context(), Topology{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Declare error = %v, want ErrClosed", err)
	}
}
