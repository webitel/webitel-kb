-- +goose Up
-- One vector column per supported dimension, each with its own ANN index: a
-- vector row fills exactly the column of its model's dimension.
ALTER TABLE kb.chunk_embedding RENAME COLUMN embedding TO embedding_768;
ALTER INDEX kb.chunk_embedding_diskann_idx RENAME TO chunk_embedding_768_idx;

ALTER TABLE kb.chunk_embedding
    ALTER COLUMN embedding_768 DROP NOT NULL,
    ADD COLUMN embedding_1024 vector(1024),
    ADD CONSTRAINT chunk_embedding_one_vector CHECK (num_nonnulls(embedding_768, embedding_1024) = 1);

CREATE INDEX chunk_embedding_1024_idx
    ON kb.chunk_embedding USING diskann (embedding_1024 vector_cosine_ops);

-- An embedding model produces a size one of the columns stores.
ALTER TABLE kb.embedding_model
    ADD CONSTRAINT embedding_model_stored_dimensions CHECK (type = 'reranker' OR dimensions IN (768, 1024));

-- +goose Down
ALTER TABLE kb.embedding_model DROP CONSTRAINT embedding_model_stored_dimensions;

DROP INDEX kb.chunk_embedding_1024_idx;

DELETE FROM kb.chunk_embedding WHERE embedding_768 IS NULL;

ALTER TABLE kb.chunk_embedding
    DROP CONSTRAINT chunk_embedding_one_vector,
    DROP COLUMN embedding_1024,
    ALTER COLUMN embedding_768 SET NOT NULL;

ALTER INDEX kb.chunk_embedding_768_idx RENAME TO chunk_embedding_diskann_idx;
ALTER TABLE kb.chunk_embedding RENAME COLUMN embedding_768 TO embedding;
