-- +goose Up
-- Access rules of spaces and articles in the platform layout. Rules flow down
-- the hierarchy: a change of a rule on a space or an article rewrites the rule
-- of that subject on every article below it, and an article is shared only
-- with a subject that may read its container. Rules keep the domain of their
-- object and subject.

CREATE TABLE kb.space_acl (
    dc      bigint   NOT NULL REFERENCES directory.wbt_domain (dc) ON DELETE CASCADE,
    grantor bigint   NULL REFERENCES directory.wbt_auth (id) ON DELETE SET NULL,
    subject bigint   NOT NULL,
    object  bigint   NOT NULL,
    access  smallint NOT NULL,
    CONSTRAINT space_acl_object_fk FOREIGN KEY (dc, object)
        REFERENCES kb.space (domain_id, id) ON DELETE CASCADE,
    CONSTRAINT space_acl_subject_fk FOREIGN KEY (dc, subject)
        REFERENCES directory.wbt_auth (dc, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX space_acl_object_subject_uindex ON kb.space_acl (object, subject) INCLUDE (access);
CREATE UNIQUE INDEX space_acl_subject_object_uindex ON kb.space_acl (subject, object) INCLUDE (access);
CREATE INDEX space_acl_grantor_index ON kb.space_acl (grantor);

CREATE TABLE kb.article_acl (
    dc      bigint   NOT NULL REFERENCES directory.wbt_domain (dc) ON DELETE CASCADE,
    grantor bigint   NULL REFERENCES directory.wbt_auth (id) ON DELETE SET NULL,
    subject bigint   NOT NULL,
    object  bigint   NOT NULL,
    access  smallint NOT NULL,
    CONSTRAINT article_acl_object_fk FOREIGN KEY (dc, object)
        REFERENCES kb.article (domain_id, id) ON DELETE CASCADE,
    CONSTRAINT article_acl_subject_fk FOREIGN KEY (dc, subject)
        REFERENCES directory.wbt_auth (dc, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX article_acl_object_subject_uindex ON kb.article_acl (object, subject) INCLUDE (access);
CREATE UNIQUE INDEX article_acl_subject_object_uindex ON kb.article_acl (subject, object) INCLUDE (access);
CREATE INDEX article_acl_grantor_index ON kb.article_acl (grantor);

-- +goose StatementBegin
-- kb.tg_obj_default_rbac gives a new object its owner and the default rules of
-- its class, as the platform one does for objects that keep the domain as dc.
CREATE FUNCTION kb.tg_obj_default_rbac() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER AS $_$
BEGIN
    EXECUTE format(
'INSERT INTO %I.%I AS acl (dc, object, grantor, subject, access)
 SELECT $1, $2, rbac.grantor, rbac.subject, rbac.access
   FROM (
    SELECT $3, $3, (255)::int2
     UNION ALL
    SELECT DISTINCT ON (rbac.subject)
        first_value(rbac.grantor) OVER sub
      , rbac.subject
      , bit_or(rbac.access) OVER sub
      FROM directory.wbt_default_acl AS rbac
      JOIN directory.wbt_class AS oc ON (oc.dc, oc.name) = ($1, %L)
     WHERE rbac.object = oc.id
       AND rbac.subject <> $3
       AND (rbac.grantor = $3 OR EXISTS (SELECT 1 FROM directory.wbt_auth_member sup
             WHERE sup.member_id = $3 AND sup.role_id = rbac.grantor))
    WINDOW sub AS (PARTITION BY rbac.subject ORDER BY rbac.access DESC)
   ) AS rbac(grantor, subject, access)',
        tg_table_schema,
        tg_table_name || '_acl',
        tg_argv[0]::name
    )
    USING NEW.domain_id, NEW.id, NEW.created_by;

    RETURN NEW;
END
$_$;

-- kb.article_below lists the articles under an article.
CREATE FUNCTION kb.article_below(root bigint) RETURNS SETOF bigint
LANGUAGE sql STABLE AS $$
    WITH RECURSIVE below AS (
        SELECT a.id FROM kb.article a WHERE a.parent_id = root
        UNION ALL
        SELECT a.id FROM kb.article a JOIN below b ON a.parent_id = b.id
    )
    SELECT id FROM below
$$;

-- kb.acl_reads reports whether a subject, by itself or its roles, may read the
-- container of an article: its parent, or its space when the space rules apply.
CREATE FUNCTION kb.acl_reads(domain bigint, above bigint, sid bigint, who bigint) RETURNS boolean
LANGUAGE sql STABLE AS $$
    WITH subjects AS (
        SELECT who AS id
        UNION
        SELECT m.role_id FROM directory.wbt_auth_member m WHERE m.member_id = who
    )
    SELECT CASE
        WHEN above IS NOT NULL THEN EXISTS (
            SELECT 1 FROM kb.article_acl acl
             WHERE acl.object = above AND acl.subject IN (SELECT id FROM subjects) AND acl.access & 4 = 4)
        ELSE NOT EXISTS (
            SELECT 1 FROM directory.wbt_class c WHERE c.dc = domain AND c.name = 'kb_spaces' AND c.rbac)
          OR EXISTS (
            SELECT 1 FROM kb.space_acl acl
             WHERE acl.object = sid AND acl.subject IN (SELECT id FROM subjects) AND acl.access & 4 = 4)
    END
$$;

-- kb.article_acl_copy gives articles the rules of their container.
CREATE FUNCTION kb.article_acl_copy(targets bigint[], above bigint, sid bigint) RETURNS void
LANGUAGE sql AS $$
    INSERT INTO kb.article_acl AS acl (dc, grantor, subject, object, access)
    SELECT src.dc, src.grantor, src.subject, t.id, src.access
      FROM unnest(targets) AS t(id)
     CROSS JOIN (
        SELECT dc, grantor, subject, access FROM kb.space_acl WHERE above IS NULL AND object = sid
        UNION ALL
        SELECT dc, grantor, subject, access FROM kb.article_acl WHERE object = above
     ) src
    ON CONFLICT (object, subject) DO UPDATE SET access = EXCLUDED.access, grantor = EXCLUDED.grantor
$$;

-- kb.tg_acl_guard checks a rule change made from outside: it locks the space
-- so the change, the articles created in it and the moves follow one order,
-- and refuses a new article rule for a subject that may not read the article
-- container.
CREATE FUNCTION kb.tg_acl_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    obj   bigint;
    sid   bigint;
    above bigint;
BEGIN
    IF pg_trigger_depth() <> 1 THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;

    obj := CASE WHEN TG_OP = 'DELETE' THEN OLD.object ELSE NEW.object END;
    IF TG_TABLE_NAME = 'space_acl' THEN
        sid := obj;
    ELSE
        SELECT a.space_id, a.parent_id INTO sid, above FROM kb.article a WHERE a.id = obj;
    END IF;

    PERFORM 1 FROM kb.space s WHERE s.id = sid FOR UPDATE;

    IF TG_TABLE_NAME = 'article_acl' AND TG_OP = 'INSERT'
       AND NOT EXISTS (SELECT 1 FROM kb.article_acl acl WHERE acl.object = NEW.object AND acl.subject = NEW.subject)
       AND NOT kb.acl_reads(NEW.dc, above, sid, NEW.subject) THEN
        RAISE EXCEPTION 'the subject may not read the space or the parent article'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'kb_article_acl_container';
    END IF;

    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END
$$;

-- kb.tg_acl_cascade rewrites a rule changed from outside on every article
-- below its object: all the articles of a space, the branch of an article.
CREATE FUNCTION kb.tg_acl_cascade() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    obj     bigint;
    targets bigint[];
BEGIN
    IF pg_trigger_depth() <> 1 THEN
        RETURN NULL;
    END IF;

    obj := CASE WHEN TG_OP = 'DELETE' THEN OLD.object ELSE NEW.object END;
    IF TG_TABLE_NAME = 'space_acl' THEN
        targets := ARRAY(SELECT a.id FROM kb.article a WHERE a.space_id = obj);
    ELSE
        targets := ARRAY(SELECT kb.article_below(obj));
    END IF;

    IF TG_OP = 'DELETE' THEN
        DELETE FROM kb.article_acl WHERE subject = OLD.subject AND object = ANY(targets);

        RETURN NULL;
    END IF;

    INSERT INTO kb.article_acl AS acl (dc, grantor, subject, object, access)
    SELECT NEW.dc, NEW.grantor, NEW.subject, t.id, NEW.access FROM unnest(targets) AS t(id)
    ON CONFLICT (object, subject) DO UPDATE SET access = EXCLUDED.access, grantor = EXCLUDED.grantor
     WHERE (acl.access, acl.grantor) IS DISTINCT FROM (EXCLUDED.access, EXCLUDED.grantor);

    RETURN NULL;
END
$$;

-- kb.tg_article_acl_inherit gives a new article the rules of its container.
CREATE FUNCTION kb.tg_article_acl_inherit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM kb.article_acl_copy(ARRAY[NEW.id], NEW.parent_id, NEW.space_id);

    RETURN NULL;
END
$$;

-- kb.tg_article_acl_rebase gives a moved branch the rules of its new container.
CREATE FUNCTION kb.tg_article_acl_rebase() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    branch bigint[] := NEW.id || ARRAY(SELECT kb.article_below(NEW.id));
BEGIN
    DELETE FROM kb.article_acl WHERE object = ANY(branch);
    PERFORM kb.article_acl_copy(branch, NEW.parent_id, NEW.space_id);

    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- A new space gets the owner and the default rules of its class.
CREATE TRIGGER tg_space_rbac AFTER INSERT ON kb.space
    FOR EACH ROW WHEN (NEW.created_by IS NOT NULL)
    EXECUTE FUNCTION kb.tg_obj_default_rbac('kb_spaces');

CREATE TRIGGER tg_space_acl_guard BEFORE INSERT OR UPDATE OR DELETE ON kb.space_acl
    FOR EACH ROW EXECUTE FUNCTION kb.tg_acl_guard();

CREATE TRIGGER tg_article_acl_guard BEFORE INSERT OR UPDATE OR DELETE ON kb.article_acl
    FOR EACH ROW EXECUTE FUNCTION kb.tg_acl_guard();

CREATE TRIGGER tg_space_acl_cascade AFTER INSERT OR UPDATE OR DELETE ON kb.space_acl
    FOR EACH ROW EXECUTE FUNCTION kb.tg_acl_cascade();

CREATE TRIGGER tg_article_acl_cascade AFTER INSERT OR UPDATE OR DELETE ON kb.article_acl
    FOR EACH ROW EXECUTE FUNCTION kb.tg_acl_cascade();

CREATE TRIGGER tg_article_acl_inherit AFTER INSERT ON kb.article
    FOR EACH ROW EXECUTE FUNCTION kb.tg_article_acl_inherit();

-- Only the root of a moved branch: its children keep their parent.
CREATE TRIGGER tg_article_acl_rebase AFTER UPDATE OF parent_id, space_id ON kb.article
    FOR EACH ROW
    WHEN (OLD.parent_id IS DISTINCT FROM NEW.parent_id
          OR (NEW.parent_id IS NULL AND OLD.space_id IS DISTINCT FROM NEW.space_id))
    EXECUTE FUNCTION kb.tg_article_acl_rebase();

-- +goose Down
DROP TRIGGER tg_article_acl_rebase ON kb.article;
DROP TRIGGER tg_article_acl_inherit ON kb.article;
DROP TRIGGER tg_article_acl_cascade ON kb.article_acl;
DROP TRIGGER tg_space_acl_cascade ON kb.space_acl;
DROP TRIGGER tg_article_acl_guard ON kb.article_acl;
DROP TRIGGER tg_space_acl_guard ON kb.space_acl;
DROP TRIGGER tg_space_rbac ON kb.space;
DROP FUNCTION kb.tg_obj_default_rbac();
DROP FUNCTION kb.tg_article_acl_rebase();
DROP FUNCTION kb.tg_article_acl_inherit();
DROP FUNCTION kb.tg_acl_cascade();
DROP FUNCTION kb.tg_acl_guard();
DROP FUNCTION kb.article_acl_copy(bigint[], bigint, bigint);
DROP FUNCTION kb.acl_reads(bigint, bigint, bigint, bigint);
DROP FUNCTION kb.article_below(bigint);
DROP TABLE kb.article_acl;
DROP TABLE kb.space_acl;
