-- +goose Up
-- The scope of a vector on its own row, so a filtered search never joins before it ranks.
ALTER TABLE kb.chunk_embedding
    ADD COLUMN domain_id bigint NOT NULL REFERENCES directory.wbt_domain (dc),
    ADD COLUMN space_id  bigint NOT NULL REFERENCES kb.space (id);

CREATE INDEX chunk_embedding_space_idx ON kb.chunk_embedding (space_id);

-- +goose Down
DROP INDEX kb.chunk_embedding_space_idx;

ALTER TABLE kb.chunk_embedding
    DROP COLUMN space_id,
    DROP COLUMN domain_id;
