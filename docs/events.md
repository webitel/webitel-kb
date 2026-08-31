# KB messaging contract: re-indexing pipeline

Contract between kb-api (Go, this repo) and kb-embedding-worker (Python) over
RabbitMQ. Source of truth for names and formats: package `internal/event`.

## 1. Flow

1. kb-api stores a new article version and, in the same transaction, inserts a
   row into `kb.outbox_events` with the complete envelope in `payload`.
2. The relay (background component of kb-api) publishes `payload` as-is to the
   `kb.reindex` exchange and advances the offset of its consumer group.
3. The worker consumes queue `kb.reindex`: chunking, embedding, atomic swap of
   the published version, `index_state` transitions.

Only a new version produces an event. Editing metadata alone (tags, state,
subject without a body, a move) leaves the indexed content valid and emits
nothing. A soft-deleted article emits nothing either: retrieval filters it out
by the article row.

## 2. Envelope `article.reindex`

Message body, JSON, UTF-8:

```json
{
  "type": "article.reindex",
  "schema": 1,
  "occurred_at": "2026-07-27T10:30:00Z",
  "article_id": 7,
  "version_id": 19,
  "space_id": 3,
  "domain_id": 1
}
```

| Field | Meaning |
|---|---|
| `type` | Message kind. Article deletion will arrive as a new type |
| `schema` | Envelope schema version. Additive changes keep it; breaking changes bump it |
| `occurred_at` | Edit time (UTC RFC3339). Start clock for indexing-lag metrics; do not use dequeue time |
| `article_id` | Swap target and routing key |
| `version_id` | Version to index |
| `space_id` | Source of `vector_search_enabled`, language, chunking strategy, embedding model |
| `domain_id` | Tenant scoping and metric labels |

Identifiers only, never document bodies; the worker reads content from the
database by `version_id`. The article's `ver` number is not transmitted.

The outbox stores the envelope as bytes; parse JSON, field order is not part
of the contract.

## 3. AMQP message properties

| Property | Value |
|---|---|
| `delivery_mode` | 2 (persistent) |
| `content_type` | `application/json` |
| routing key | decimal `article_id`, single word, no dots |
| header `x-message-id` | outbox message uuid; stable across republishes; tracing only, NOT a dedup key |

No other header is part of the contract.

## 4. Topology

Both sides declare every object with exactly these values. A redeclare with
different properties fails with 406 and closes the channel.

| Object | Parameters |
|---|---|
| exchange `kb.reindex` | type=topic, durable=true, auto_delete=false, internal=false, arguments={} |
| queue `kb.reindex` | durable=true, auto_delete=false, exclusive=false, arguments={"x-dead-letter-exchange": "kb.reindex.dlx"} |
| binding | queue `kb.reindex` to exchange `kb.reindex`, key `#` |
| exchange `kb.reindex.dlx` | type=fanout, durable=true, auto_delete=false, internal=false, arguments={} |
| queue `kb.reindex.dlq` | durable=true, auto_delete=false, exclusive=false, arguments={} |
| binding | queue `kb.reindex.dlq` to exchange `kb.reindex.dlx`, key `` (empty) |

The exchange and the queue intentionally share the name `kb.reindex`. Publish
only to the exchange; publishing to the default exchange with routing key
`kb.reindex` bypasses the topic exchange and is an integration error.

The relay publishes with `mandatory=true`: an unroutable message is a publish
error, never a silent drop. It redeclares the topology on every reconnect, so a
wiped or replaced broker recovers without a restart.

Changing an exchange type or queue arguments in place is impossible: it is a
delete+recreate ops step.

## 5. Relay leadership

Exactly one kb-api instance relays at a time. Leadership is a Consul KV lock:
the instance holds a session (TTL `10s`, behavior `release`) on the key
`service/webitel-kb/leader`, whose value is the instance id. The session is
renewed every `5s`, and standby instances block on the key, so a released key is
picked up at once rather than on a poll.

Two Consul rules shape the numbers: an abandoned session is invalidated at up to
**twice** its TTL, and a lock delay of zero is read as "unset" and replaced by a
15s default. Hence the ten second TTL (the Consul minimum) and an explicitly
tiny lock delay: an unclean death costs up to 20s of standstill, well inside the
re-index lag budget.

What follows from it:

- a graceful stop destroys the session and frees the key at once, so a rolling
  restart barely pauses publishing;
- at a handover an in-flight message may be published twice, and outbox order
  may be crossed exactly at that boundary. Both are covered by the worker
  idempotency;

## 6. Delivery and idempotency

At-least-once; duplicates are normal (crash between publish and mark, relay
leader change with a publish in flight). Normative worker idempotency, NOT
message_id dedup:

- chunk insert is idempotent by uniqueness of `(version_id, chunk_index, model_id)`;
- published-version swap is monotonic (a stale job cannot roll back a newer version);
- retention cleanup of previous-version chunks runs only AFTER a successful swap.

## 7. Consumption: ack, retry, DLQ

- `basic_ack` manually AFTER processing completes.
- Transient errors (timeout/429/5xx): up to 5 in-process retries with
  exponential backoff.
- After exhausted retries or on a permanent error: set the article's
  `index_state=4`, then `basic_nack(requeue=False)` — the message dead-letters
  into `kb.reindex.dlq`. `requeue=True` loops forever and never reaches the DLQ.
- `prefetch_count=1` in v1 (ordering).
- DLQ carries the `x-death` header. Redrive is manual (fix the cause, republish
  to `kb.reindex`). No automatic redrive in v1.

## 8. Ordering

Guaranteed by "one active relay publishes in commit order" plus
"one consumer with prefetch=1". Multiple worker instances are allowed for failover;
only one consumes.

Commit order, not insert order: the relay reads by `(transaction_id, offset)`.
The outbox row must still be written in the same transaction as the article
change, for atomicity, but ordering no longer depends on it.

Seeding scale-out: adding consumers is allowed and deliberately breaks
per-article FIFO. Safe: the monotonic swap guard decides correctness; a losing
job leaves at most orphan chunks of its own version. Do NOT build cross-instance
ordering coordination.

## 9. Schema evolution

Consumer: ignore unknown fields; unknown `type` or `schema` greater than
supported: `basic_nack(requeue=False)` to DLQ with a log.

Producer: additive fields keep `schema`; changing or removing a field bumps it
and requires worker sign-off before release.

## 10. `kb.article.index_state`

Numeric codes are authoritative:

| Code | State | Written by |
|---|---|---|
| 1 | pending | kb-api on version save |
| 2 | indexing | worker at job start |
| 3 | indexed | worker after successful swap |
| 4 | failed | worker before nack to DLQ; relay when delivery is given up |

The relay stamps code 4 before setting an undeliverable envelope aside and
acknowledging it: without the stamp the article stays pending with nothing left
to deliver it.

## 11. Outbox retention

The relay owns the outbox table.

| Parameter | Meaning |
|---|---|
| `relay.retention` | minimum age of a deletable row |
| `relay.cleanup_interval` | how often cleanup runs |
| `relay.cleanup_batch` | rows per delete statement |

Only rows at or below the acknowledged offset of the consumer group are
deleted. A stalled relay costs disk, never delivery.
