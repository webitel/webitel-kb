// Package reliable is a minimal AMQP publisher with broker confirmations:
// Publish returns only after the broker acknowledges the message and reports
// an unroutable one as an error, so a nil error means "accepted into a queue",
// not "written to the socket". The connection is dialed lazily and rebuilt on
// failure, so constructing a Publisher never blocks on an unavailable broker.
//
// Blocking bounds: the confirmation wait respects ctx; every socket write
// carries writeTimeout (a broker applying TCP pushback cannot hang a call
// forever); teardown closes with a deadline; and a silent broker is detected
// by the AMQP heartbeat, which fails all in-flight waits.
package reliable

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	// connectTimeout bounds one dial attempt; the AMQP handshake runs under
	// the same deadline.
	connectTimeout = 10 * time.Second

	// writeTimeout bounds every socket write after the handshake.
	writeTimeout = 30 * time.Second

	// closeTimeout bounds the close handshake during teardown.
	closeTimeout = 5 * time.Second
)

// ErrClosed reports use of a Publisher after Close.
var ErrClosed = errors.New("reliable: publisher is closed")

// Message is one publication.
type Message struct {
	Body        []byte
	MessageID   string
	ContentType string
}

// Topology declares broker objects. Every object is durable, non-auto-delete
// and non-internal by construction; only the properties that legitimately vary
// are configurable.
type Topology struct {
	Exchanges []Exchange
	Queues    []Queue
	Bindings  []Binding
}

type Exchange struct {
	Name string
	Kind string
}

type Queue struct {
	Name string
	Args map[string]any
}

type Binding struct {
	Queue    string
	Exchange string
	Key      string
}

// Publisher is safe for concurrent use; publications are serialized, which
// keeps one broker confirmation in flight at a time.
type Publisher struct {
	url string

	mu       sync.Mutex
	conn     *amqp.Connection
	ch       *amqp.Channel
	confirms <-chan amqp.Confirmation
	returns  <-chan amqp.Return
	closed   bool
}

// NewPublisher prepares a publisher for the given amqp:// URL without dialing.
func NewPublisher(url string) *Publisher {
	return &Publisher{url: url}
}

// Publish sends one persistent mandatory message and waits for the broker
// confirmation. ctx bounds the confirmation wait; on timeout the channel state
// is unknown, so the connection is torn down and rebuilt by the next call. A
// confirmed message that matched no queue binding comes back as a return and
// is reported as an error rather than silently dropped.
func (p *Publisher) Publish(ctx context.Context, exchange, routingKey string, msg Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, err := p.ensure()
	if err != nil {
		return err
	}

	err = ch.PublishWithContext(ctx, exchange, routingKey, true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType:  msg.ContentType,
		MessageId:    msg.MessageID,
		Body:         msg.Body,
	})
	if err != nil {
		p.teardown()

		return fmt.Errorf("reliable: publish: %w", err)
	}

	select {
	case confirm, ok := <-p.confirms:
		if !ok {
			p.teardown()

			return errors.New("reliable: connection lost awaiting confirm")
		}

		if !confirm.Ack {
			return errors.New("reliable: broker rejected the publish")
		}

		// The broker sends a return before the confirmation of the same
		// message, so by now an unroutable publish is already buffered.
		select {
		case ret := <-p.returns:
			return fmt.Errorf("reliable: message unroutable: %s (%d)", ret.ReplyText, ret.ReplyCode)
		default:
		}

		return nil
	case <-ctx.Done():
		// An unread confirmation would be attributed to the next publish;
		// dropping the channel keeps the accounting unambiguous.
		p.teardown()

		return fmt.Errorf("reliable: await confirm: %w", ctx.Err())
	}
}

// Declare creates the topology objects; existing objects with identical
// properties are accepted, a property mismatch fails the channel. The declare
// round trips are bounded by the write timeout and heartbeat detection rather
// than ctx: the AMQP synchronous calls cannot carry a context.
func (p *Publisher) Declare(_ context.Context, topology Topology) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, err := p.ensure()
	if err != nil {
		return err
	}

	for _, e := range topology.Exchanges {
		if err := ch.ExchangeDeclare(e.Name, e.Kind, true, false, false, false, nil); err != nil {
			p.teardown()

			return fmt.Errorf("reliable: declare exchange %s: %w", e.Name, err)
		}
	}

	for _, q := range topology.Queues {
		if _, err := ch.QueueDeclare(q.Name, true, false, false, false, amqp.Table(q.Args)); err != nil {
			p.teardown()

			return fmt.Errorf("reliable: declare queue %s: %w", q.Name, err)
		}
	}

	for _, b := range topology.Bindings {
		if err := ch.QueueBind(b.Queue, b.Key, b.Exchange, false, nil); err != nil {
			p.teardown()

			return fmt.Errorf("reliable: bind %s to %s: %w", b.Queue, b.Exchange, err)
		}
	}

	return nil
}

// Close tears the connection down; in-flight and future calls fail with
// ErrClosed.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	p.teardown()

	return nil
}

// ensure returns a live confirming channel, dialing if needed. Callers hold mu.
func (p *Publisher) ensure() (*amqp.Channel, error) {
	if p.closed {
		return nil, ErrClosed
	}

	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}

	p.teardown()

	conn, err := amqp.DialConfig(p.url, amqp.Config{Dial: dial})
	if err != nil {
		return nil, fmt.Errorf("reliable: dial: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.CloseDeadline(time.Now().Add(closeTimeout))

		return nil, fmt.Errorf("reliable: open channel: %w", err)
	}

	if err := ch.Confirm(false); err != nil {
		_ = conn.CloseDeadline(time.Now().Add(closeTimeout))

		return nil, fmt.Errorf("reliable: enable confirms: %w", err)
	}

	p.conn = conn
	p.ch = ch
	p.confirms = ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	p.returns = ch.NotifyReturn(make(chan amqp.Return, 1))

	return p.ch, nil
}

// dial establishes the TCP connection under the handshake deadline and bounds
// every later write, so a broker applying TCP pushback cannot block a publish
// or a close forever.
func dial(network, addr string) (net.Conn, error) {
	conn, err := amqp.DefaultDial(connectTimeout)(network, addr)
	if err != nil {
		return nil, err
	}

	return &writeDeadlineConn{Conn: conn}, nil
}

// writeDeadlineConn stamps a deadline on each write; reads stay unbounded (the
// AMQP heartbeat detects a silent peer).
type writeDeadlineConn struct {
	net.Conn
}

func (c *writeDeadlineConn) Write(b []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return 0, err
	}

	return c.Conn.Write(b)
}

// teardown drops the connection state with a bounded close handshake. Callers
// hold mu.
func (p *Publisher) teardown() {
	if p.conn != nil {
		// Closing the connection closes its channels and confirm listeners.
		_ = p.conn.CloseDeadline(time.Now().Add(closeTimeout))
	}

	p.conn = nil
	p.ch = nil
	p.confirms = nil
	p.returns = nil
}
