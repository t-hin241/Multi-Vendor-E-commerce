-- Rewrites stored image URLs from one public base to another, for example
-- from http://localhost:9000 (MinIO published directly) to
-- https://<domain>/media (behind Caddy, see edge-runbook.md).
--
-- Run once per database (catalog_db, vendor_db, review_db), normally
-- through deploy/rewrite-media-urls.sh:
--
--   psql -v old_base=http://localhost:9000 -v new_base=https://shop.example.com/media \
--        -v apply=false -f deploy/rewrite-media-urls.sql <database>
--
-- Every uploaded URL is <base>/<bucket>/<object_key> (pkg/platform/
-- objectstorage), so only a row whose URL is exactly old_base/bucket/key of
-- its own object key is changed. Anything else (an external image, another
-- host) is counted as "other", listed by prefix, and left alone. Rerunning
-- changes nothing; swapping old_base and new_base reverts the change.
--
-- apply=false (default) reports inside a transaction that is rolled back.
\set ON_ERROR_STOP on
\if :{?old_base}
\else
DO $$ BEGIN RAISE EXCEPTION 'missing -v old_base=...'; END $$;
\endif
\if :{?new_base}
\else
DO $$ BEGIN RAISE EXCEPTION 'missing -v new_base=...'; END $$;
\endif
\if :{?apply}
\else
\set apply false
\endif

BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

-- Columns holding an uploaded URL, with the object key column next to it and
-- the bucket the service uploads to (docker-compose.yml). Tables missing in
-- the current database are skipped, so one script serves all three.
CREATE TEMP TABLE media_url_targets (tbl text, url_col text, key_col text, bucket text) ON COMMIT DROP;
INSERT INTO media_url_targets VALUES
    ('product_images', 'url', 'object_key', 'product-images'),
    ('product_media', 'url', 'object_key', 'product-images'),
    ('vendors', 'logo_url', 'logo_object_key', 'vendor-branding'),
    ('vendors', 'banner_url', 'banner_object_key', 'vendor-branding'),
    ('review_images', 'url', 'object_key', 'review-images');

CREATE FUNCTION pg_temp.media_url_rewrite(old_base text, new_base text, apply boolean)
RETURNS TABLE (target text, rewritten bigint, already_new bigint, other bigint)
LANGUAGE plpgsql AS $$
DECLARE
    t record;
    old_prefix text := rtrim(old_base, '/');
    new_prefix text := rtrim(new_base, '/');
    changed bigint;
BEGIN
    IF old_prefix !~ '^https?://[^/?#\s]+(/[^?#\s]*)?$' THEN
        RAISE EXCEPTION 'old_base must be an absolute http(s) URL without query: %', old_base;
    END IF;
    IF new_prefix !~ '^https?://[^/?#\s]+(/[^?#\s]*)?$' THEN
        RAISE EXCEPTION 'new_base must be an absolute http(s) URL without query: %', new_base;
    END IF;
    IF old_prefix = new_prefix THEN
        RAISE EXCEPTION 'old_base and new_base are the same';
    END IF;

    FOR t IN SELECT m.* FROM media_url_targets m WHERE to_regclass(m.tbl) IS NOT NULL ORDER BY m.tbl, m.url_col LOOP
        target := t.tbl || '.' || t.url_col;
        EXECUTE format(
            'SELECT count(*) FILTER (WHERE %1$I = $1 || ''/'' || %3$L || ''/'' || %2$I),
                    count(*) FILTER (WHERE %1$I = $2 || ''/'' || %3$L || ''/'' || %2$I),
                    count(*) FILTER (WHERE %1$I IS NOT NULL
                                       AND %1$I IS DISTINCT FROM $1 || ''/'' || %3$L || ''/'' || %2$I
                                       AND %1$I IS DISTINCT FROM $2 || ''/'' || %3$L || ''/'' || %2$I)
               FROM %4$I',
            t.url_col, t.key_col, t.bucket, t.tbl)
            INTO rewritten, already_new, other
            USING old_prefix, new_prefix;

        IF apply AND rewritten > 0 THEN
            EXECUTE format(
                'UPDATE %4$I SET %1$I = $2 || ''/'' || %3$L || ''/'' || %2$I
                  WHERE %1$I = $1 || ''/'' || %3$L || ''/'' || %2$I',
                t.url_col, t.key_col, t.bucket, t.tbl)
                USING old_prefix, new_prefix;
            GET DIAGNOSTICS changed = ROW_COUNT;
            IF changed <> rewritten THEN
                RAISE EXCEPTION '%: counted % rows to rewrite but updated %', target, rewritten, changed;
            END IF;
        END IF;
        RETURN NEXT;
    END LOOP;
END
$$;

-- URLs this run does not touch, grouped by what precedes /<bucket>/<key>
-- (or "unrecognised" when the URL does not end with its own object key):
-- another old host to rerun with, or images that never lived in the bucket.
CREATE FUNCTION pg_temp.media_url_other(old_base text, new_base text)
RETURNS TABLE (target text, prefix text, row_count bigint)
LANGUAGE plpgsql AS $$
DECLARE
    t record;
BEGIN
    FOR t IN SELECT m.* FROM media_url_targets m WHERE to_regclass(m.tbl) IS NOT NULL ORDER BY m.tbl, m.url_col LOOP
        RETURN QUERY EXECUTE format(
            'SELECT %5$L::text,
                    CASE WHEN right(%1$I, length(''/'' || %3$L || ''/'' || %2$I)) = ''/'' || %3$L || ''/'' || %2$I
                         THEN left(%1$I, length(%1$I) - length(''/'' || %3$L || ''/'' || %2$I))
                         ELSE ''unrecognised'' END,
                    count(*)
               FROM %4$I
              WHERE %1$I IS NOT NULL
                AND %1$I IS DISTINCT FROM $1 || ''/'' || %3$L || ''/'' || %2$I
                AND %1$I IS DISTINCT FROM $2 || ''/'' || %3$L || ''/'' || %2$I
              GROUP BY 2 ORDER BY 3 DESC LIMIT 20',
            t.url_col, t.key_col, t.bucket, t.tbl, t.tbl || '.' || t.url_col)
            USING rtrim(old_base, '/'), rtrim(new_base, '/');
    END LOOP;
END
$$;

\echo
\echo '== media URL rewrite' :DBNAME 'apply=' :apply
SELECT * FROM pg_temp.media_url_rewrite(:'old_base', :'new_base', :'apply'::boolean);
\echo '== URLs left unchanged (by prefix)'
SELECT * FROM pg_temp.media_url_other(:'old_base', :'new_base');

\if :apply
COMMIT;
\echo 'committed'
\else
ROLLBACK;
\echo 'dry run: rolled back'
\endif
