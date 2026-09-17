-- +goose Up
-- Related cases are kept by the cases service.
DROP TABLE kb.article_case;

-- +goose Down
CREATE TABLE kb.article_case (
    article_id bigint NOT NULL REFERENCES kb.article (id) ON DELETE CASCADE,
    case_id    bigint NOT NULL REFERENCES cases."case" (id) ON DELETE CASCADE,
    source     smallint NOT NULL,  -- 1=manual, 2=resolution
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by bigint NULL,
    PRIMARY KEY (article_id, case_id)
);
