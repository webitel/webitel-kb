-- +goose Up
-- The relay reads pending rows in id order; the index must serve
-- "WHERE published_at IS NULL ORDER BY id LIMIT n" without a sort.
DROP INDEX kb.outbox_unpublished_idx;
CREATE INDEX outbox_unpublished_idx ON kb.outbox_events (id) WHERE published_at IS NULL;

-- +goose Down
DROP INDEX kb.outbox_unpublished_idx;
CREATE INDEX outbox_unpublished_idx ON kb.outbox_events (published_at) WHERE published_at IS NULL;
