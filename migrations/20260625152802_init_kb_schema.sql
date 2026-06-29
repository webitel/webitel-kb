-- +goose Up
CREATE SCHEMA kb;

CREATE TABLE kb.embedding_model (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    domain_id      bigint NULL REFERENCES directory.wbt_domain (dc),  -- null = global model
    kind           text NOT NULL DEFAULT 'embedding'
                       CHECK (kind IN ('embedding', 'reranker')),
    name           text NOT NULL,
    provider       text NOT NULL
                       CHECK (provider IN ('gemini', 'openai', 'cohere', 'azure',
                                           'bge-m3', 'e5', 'bge-reranker', 'byom')),
    is_self_hosted boolean NOT NULL DEFAULT false,
    model_ref      text NULL,            -- HF id or provider model name
    dimensions     int NULL,             -- embedding dim; null for reranker
    endpoint       text NULL,            -- self-hosted / Azure / BYOM url
    config         bytea NULL,           -- encrypted API key; null for self-hosted
    validated_at   timestamptz NULL,     -- set after a successful test call
    created_at     timestamptz NOT NULL DEFAULT now(),
    created_by     bigint NULL,
    CHECK (kind = 'reranker' OR dimensions IS NOT NULL),
    UNIQUE NULLS NOT DISTINCT (domain_id, name)
);

CREATE TABLE kb.space (
    id                        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    domain_id                 bigint NOT NULL REFERENCES directory.wbt_domain (dc),
    name                      text NOT NULL,
    description               text NULL,
    language                  text NOT NULL,  -- immutable; drives full-text search config
    embedding_model_id        bigint NULL REFERENCES kb.embedding_model (id),
    target_embedding_model_id bigint NULL REFERENCES kb.embedding_model (id),  -- model-migration target
    reranker_model_id         bigint NULL REFERENCES kb.embedding_model (id),
    vector_search_enabled     boolean NOT NULL DEFAULT true,
    rerank_enabled            boolean NOT NULL DEFAULT false,
    chunking_strategy         text NOT NULL DEFAULT 'recursive_markdown',
    home_article_id           bigint NULL,    -- article used as the space home page
    created_at                timestamptz NOT NULL DEFAULT now(),
    created_by                bigint NULL,
    updated_at                timestamptz NOT NULL DEFAULT now(),
    updated_by                bigint NULL,
    deleted_at                timestamptz NULL
);

CREATE TABLE kb.article (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    space_id             bigint NOT NULL REFERENCES kb.space (id) ON DELETE RESTRICT,
    parent_id            bigint NULL REFERENCES kb.article (id) ON DELETE CASCADE,  -- null = top level
    depth                smallint NOT NULL CHECK (depth BETWEEN 1 AND 5),
    type                 smallint NOT NULL DEFAULT 1,  -- 1=article, 2=faq
    subject              text NOT NULL,  -- current subject (mirrors published version)
    tags                 text[] NOT NULL DEFAULT '{}',
    state                smallint NOT NULL DEFAULT 1,  -- 1=draft, 2=active, 3=inactive
    index_state          smallint NOT NULL DEFAULT 1,  -- 1=pending, 2=indexing, 3=indexed, 4=failed
    ver                  int NOT NULL DEFAULT 0,  -- optimistic lock
    published_version_id bigint NULL,             -- pointer to the live version
    created_at           timestamptz NOT NULL DEFAULT now(),
    created_by           bigint NULL,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    updated_by           bigint NULL,
    deleted_at           timestamptz NULL
);
CREATE INDEX article_tags_gin_idx ON kb.article USING gin (tags);
CREATE INDEX article_space_parent_idx ON kb.article (space_id, parent_id);
CREATE INDEX article_space_depth_idx ON kb.article (space_id, depth);
CREATE INDEX article_parent_idx ON kb.article (parent_id);

CREATE TABLE kb.article_version (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    article_id     bigint NOT NULL REFERENCES kb.article (id) ON DELETE CASCADE,
    version_number int NOT NULL,
    subject        text NOT NULL,
    body_rich_text jsonb NOT NULL,     -- canonical editor document
    body_markdown  text NOT NULL,      -- chunking input
    body_plain     text NOT NULL,      -- full-text search source
    tsv            tsvector NOT NULL,  -- materialized full-text vector
    restored_from  bigint NULL REFERENCES kb.article_version (id),
    notes          text NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    created_by     bigint NULL,        -- version author
    UNIQUE (article_id, version_number)
);
CREATE INDEX article_version_tsv_gin_idx ON kb.article_version USING gin (tsv);
CREATE INDEX article_version_trgm_gin_idx ON kb.article_version USING gin (body_plain gin_trgm_ops);

ALTER TABLE kb.space
    ADD CONSTRAINT space_home_article_fk
    FOREIGN KEY (home_article_id) REFERENCES kb.article (id) ON DELETE SET NULL;
ALTER TABLE kb.article
    ADD CONSTRAINT article_published_version_fk
    FOREIGN KEY (published_version_id) REFERENCES kb.article_version (id);

CREATE TABLE kb.chunk (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version_id  bigint NOT NULL REFERENCES kb.article_version (id) ON DELETE CASCADE,
    chunk_index int NOT NULL,
    content     text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (version_id, chunk_index)  -- idempotent reindex
);

CREATE TABLE kb.chunk_embedding (
    chunk_id   bigint NOT NULL REFERENCES kb.chunk (id) ON DELETE CASCADE,
    model_id   bigint NOT NULL REFERENCES kb.embedding_model (id),
    embedding  vector(768) NOT NULL,  -- active model dimension
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (chunk_id, model_id)
);
CREATE INDEX chunk_embedding_diskann_idx
    ON kb.chunk_embedding USING diskann (embedding vector_cosine_ops);

CREATE TABLE kb.team_space (
    team_id    bigint NOT NULL REFERENCES call_center.cc_team (id) ON DELETE CASCADE,
    space_id   bigint NOT NULL REFERENCES kb.space (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by bigint NULL,
    PRIMARY KEY (team_id, space_id)
);
CREATE INDEX team_space_space_idx ON kb.team_space (space_id);

CREATE TABLE kb.article_case (
    article_id bigint NOT NULL REFERENCES kb.article (id) ON DELETE CASCADE,
    case_id    bigint NOT NULL REFERENCES cases."case" (id) ON DELETE CASCADE,
    source     smallint NOT NULL,  -- 1=manual, 2=resolution
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by bigint NULL,
    PRIMARY KEY (article_id, case_id)
);

CREATE TABLE kb.outbox_events (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    aggregate_id bigint NOT NULL,  -- article id
    event_type   text NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz NULL   -- null until relayed to the queue
);
CREATE INDEX outbox_unpublished_idx ON kb.outbox_events (published_at) WHERE published_at IS NULL;

-- +goose Down
DROP SCHEMA IF EXISTS kb CASCADE;
