-- +goose Up
-- Space lifecycle state: 1=active, 2=inactive; existing spaces stay active.
ALTER TABLE kb.space
    ADD COLUMN state smallint NOT NULL DEFAULT 1,
    ADD CONSTRAINT space_state_check CHECK (state IN (1, 2));

-- +goose Down
ALTER TABLE kb.space
    DROP CONSTRAINT space_state_check,
    DROP COLUMN state;
