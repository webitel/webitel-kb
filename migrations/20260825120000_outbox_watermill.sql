-- +goose Up
-- The outbox is read by a watermill-sql subscriber.
DROP TABLE kb.outbox_events;

CREATE TABLE kb.outbox_events (
    "offset"         bigserial,
    "uuid"           varchar(36) NOT NULL,
    "created_at"     timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "payload"        bytea DEFAULT NULL,
    "metadata"       json DEFAULT NULL,
    "transaction_id" xid8 NOT NULL,
    PRIMARY KEY ("transaction_id", "offset")
);

CREATE TABLE kb.outbox_offsets (
    consumer_group                varchar(255) NOT NULL,
    offset_acked                  bigint,
    last_processed_transaction_id xid8 NOT NULL,
    PRIMARY KEY (consumer_group)
);

-- +goose Down
DROP TABLE kb.outbox_offsets;
DROP TABLE kb.outbox_events;

CREATE TABLE kb.outbox_events (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    aggregate_id bigint NOT NULL,
    event_type   text NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz NULL
);

CREATE INDEX outbox_unpublished_idx ON kb.outbox_events (published_at) WHERE published_at IS NULL;
