-- +goose Up
-- Files of an article. The bytes and their lifecycle stay in Storage; this is
-- the binding plus the metadata a listing needs.
CREATE TABLE kb.attachment (
    article_id bigint      NOT NULL REFERENCES kb.article (id) ON DELETE CASCADE,
    file_id    bigint      NOT NULL,  -- Storage file id
    name       text        NOT NULL,
    mime       text        NULL,
    size       bigint      NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by bigint      NULL,
    PRIMARY KEY (article_id, file_id)
);

-- Unbinding asks whether the file is still bound anywhere.
CREATE INDEX attachment_file_id_idx ON kb.attachment (file_id);

-- +goose Down
DROP TABLE kb.attachment;
