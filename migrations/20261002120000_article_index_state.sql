-- +goose Up
-- Live articles by index state, counted by the relay leader for the metrics.
CREATE INDEX article_index_state_idx ON kb.article (index_state) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX kb.article_index_state_idx;
