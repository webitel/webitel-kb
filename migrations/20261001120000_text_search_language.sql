-- +goose Up
-- Search configurations of the kb: every built-in stemmer, and simple for the
-- rest, each folding accents before the dictionary sees a word.
-- +goose StatementBegin
DO $$
DECLARE
    stemmer record;
BEGIN
    CREATE TEXT SEARCH CONFIGURATION kb.simple (COPY = pg_catalog.simple);
    ALTER TEXT SEARCH CONFIGURATION kb.simple
        ALTER MAPPING FOR word, hword, hword_part WITH unaccent, simple;

    FOR stemmer IN
        SELECT c.cfgname, d.dictname
        FROM pg_ts_config c
        JOIN pg_ts_config_map m ON m.mapcfg = c.oid AND m.mapseqno = 1
        JOIN pg_ts_dict d ON d.oid = m.mapdict
        WHERE c.cfgnamespace = 'pg_catalog'::regnamespace
          AND c.cfgname <> 'simple'
          AND m.maptokentype = (SELECT tokid FROM ts_token_type('default') WHERE alias = 'word')
    LOOP
        EXECUTE format('CREATE TEXT SEARCH CONFIGURATION kb.%I (COPY = pg_catalog.%I)',
                       stemmer.cfgname, stemmer.cfgname);
        EXECUTE format('ALTER TEXT SEARCH CONFIGURATION kb.%I ALTER MAPPING FOR word, hword, hword_part WITH unaccent, %I',
                       stemmer.cfgname, stemmer.dictname);
    END LOOP;
END $$;
-- +goose StatementEnd

-- The search configuration of the space language; immutable like the language.
-- The default only covers rows written before the column existed.
ALTER TABLE kb.space ADD COLUMN text_search_config text NOT NULL DEFAULT 'kb.simple';
ALTER TABLE kb.space ALTER COLUMN text_search_config DROP DEFAULT;

-- The writer of a row builds its vectors under the space configuration.
ALTER TABLE kb.article ALTER COLUMN search_tsv DROP EXPRESSION;
ALTER TABLE kb.chunk ALTER COLUMN tsv DROP EXPRESSION;

-- Typo tolerant matching of subjects.
CREATE INDEX article_subject_trgm_gin_idx ON kb.article USING gin (subject gin_trgm_ops);

-- +goose Down
DROP INDEX kb.article_subject_trgm_gin_idx;

ALTER TABLE kb.chunk DROP COLUMN tsv;
ALTER TABLE kb.chunk
    ADD COLUMN tsv tsvector
        GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, content)) STORED;
CREATE INDEX chunk_tsv_gin_idx ON kb.chunk USING gin (tsv);

ALTER TABLE kb.article DROP COLUMN search_tsv;
ALTER TABLE kb.article
    ADD COLUMN search_tsv tsvector
        GENERATED ALWAYS AS (setweight(to_tsvector('simple'::regconfig, subject), 'A')) STORED;
CREATE INDEX article_search_tsv_gin_idx ON kb.article USING gin (search_tsv);

ALTER TABLE kb.space DROP COLUMN text_search_config;

-- +goose StatementBegin
DO $$
DECLARE
    config record;
BEGIN
    FOR config IN SELECT cfgname FROM pg_ts_config WHERE cfgnamespace = 'kb'::regnamespace LOOP
        EXECUTE format('DROP TEXT SEARCH CONFIGURATION kb.%I', config.cfgname);
    END LOOP;
END $$;
-- +goose StatementEnd
