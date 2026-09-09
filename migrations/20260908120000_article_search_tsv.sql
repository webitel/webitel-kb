-- +goose Up
-- The subject vector of an article, weighted above the body vector of its version.
ALTER TABLE kb.article
    ADD COLUMN search_tsv tsvector
        GENERATED ALWAYS AS (setweight(to_tsvector('simple'::regconfig, subject), 'A')) STORED;

CREATE INDEX article_search_tsv_gin_idx ON kb.article USING gin (search_tsv);

-- +goose Down
DROP INDEX kb.article_search_tsv_gin_idx;

ALTER TABLE kb.article DROP COLUMN search_tsv;
