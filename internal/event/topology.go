package event

import "strconv"

// Broker topology for the re-indexing pipeline. Both sides declare it with
// exactly these parameters — every object durable, non-auto-delete,
// non-internal — because a redeclare with different properties fails the
// channel, not just the call. The full contract, including the consumer-side
// rules, lives in docs/events.md.
const (
	// ReindexExchange fans article.reindex envelopes out to the indexing
	// queue. Topic type: the routing key is the article id, so a future
	// switch to partitioned consumption changes only the binding side.
	ReindexExchange     = "kb.reindex"
	ReindexExchangeType = "topic"

	// ReindexQueue is the single v1 indexing queue, bound to every routing
	// key. Rejected deliveries dead-letter into ReindexDLX.
	ReindexQueue        = "kb.reindex"
	ReindexQueueBinding = "#"

	// ReindexDLX and ReindexDLQ collect envelopes the worker gave up on.
	ReindexDLX     = "kb.reindex.dlx"
	ReindexDLXType = "fanout"
	ReindexDLQ     = "kb.reindex.dlq"

	// ReindexContentType is the content type of every published envelope.
	ReindexContentType = "application/json"

	// ReindexMessageIDHeader carries the outbox message id for tracing. It is
	// a header rather than the AMQP message_id property because the shared
	// publisher does not expose message properties.
	ReindexMessageIDHeader = "x-message-id"
)

// ReindexQueueArgs returns the declare arguments of the indexing queue.
func ReindexQueueArgs() map[string]any {
	return map[string]any{"x-dead-letter-exchange": ReindexDLX}
}

// ReindexRoutingKey renders the routing key of one article's envelopes: the
// decimal article id, a single word with no dots.
func ReindexRoutingKey(articleID int64) string {
	return strconv.FormatInt(articleID, 10)
}
