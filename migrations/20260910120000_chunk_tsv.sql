-- +goose Up
-- The lexical vector of a chunk, for the full-text side of hybrid retrieval.
ALTER TABLE kb.chunk
    ADD COLUMN tsv tsvector
        GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, content)) STORED;

CREATE INDEX chunk_tsv_gin_idx ON kb.chunk USING gin (tsv);

-- +goose Down
DROP INDEX kb.chunk_tsv_gin_idx;

ALTER TABLE kb.chunk DROP COLUMN tsv;
