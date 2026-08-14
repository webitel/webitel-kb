-- +goose Up
-- The article enums are code-owned smallints; without a check the columns
-- accept any number, including the unspecified zero of the API contract.
ALTER TABLE kb.article
    ADD CONSTRAINT article_type_check CHECK (type IN (1, 2)),
    ADD CONSTRAINT article_state_check CHECK (state IN (1, 2, 3)),
    ADD CONSTRAINT article_index_state_check CHECK (index_state IN (1, 2, 3, 4));

-- +goose Down
ALTER TABLE kb.article
    DROP CONSTRAINT article_type_check,
    DROP CONSTRAINT article_state_check,
    DROP CONSTRAINT article_index_state_check;
