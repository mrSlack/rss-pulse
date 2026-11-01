-- Article search key (see docs/PROJECT.md, "Performance notes"): the lower-cased words of
-- the title and the plain-text summary joined by the letter ǂ (U+01C2), so that the
-- trigram index also covers word boundaries and phrase searches stay selective.
--
-- This migration changes only the catalog. Ingestion stores the Go value
-- (store.ArticleSearchKey); existing rows are filled in batches and the trigram index is
-- built concurrently after the API has started (store.ArticleSearchUpgrade). Until then,
-- search matches COALESCE(search_key, pulse_article_search_key(title, summary)), the
-- same normalization in SQL, so it is correct for rows without a stored key.
--
-- Databases that applied the first version of 016 have articles.search_text (space-
-- separated words) and its trigram index; dropping the column drops the index and frees
-- the space when rows are next rewritten.
ALTER TABLE articles DROP COLUMN IF EXISTS search_text;
ALTER TABLE articles ADD COLUMN IF NOT EXISTS search_key TEXT;

-- Searches that scan many articles spend most of their time decompressing the key (rows
-- with a summary exceed the 2 kB TOAST threshold, so the key is stored compressed);
-- LZ4 decompresses about twice as fast as the default pglz. It applies to values written
-- from now on. PostgreSQL builds without LZ4 keep pglz.
DO $$
BEGIN
    ALTER TABLE articles ALTER COLUMN search_key SET COMPRESSION lz4;
EXCEPTION WHEN feature_not_supported THEN
    RAISE NOTICE 'LZ4 is not available; articles.search_key keeps the default compression';
END
$$;

-- IMMUTABLE because the trigram index covers an expression using it. PL/pgSQL keeps the
-- planner from inlining it, so a plan is costed for the stored key (COALESCE evaluates
-- the function only for rows without one), and COST 1 says so.
CREATE OR REPLACE FUNCTION pulse_article_search_key(title TEXT, summary TEXT) RETURNS TEXT
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE COST 1
AS $$
BEGIN
    RETURN replace(btrim(regexp_replace(lower(replace(
        title || ' ' ||
        replace(replace(replace(replace(replace(replace(replace(
            regexp_replace(summary, '<[^>]*>', ' ', 'g'),
            '&nbsp;', ' '), chr(160), ' '), '&lt;', '<'), '&gt;', '>'), '&quot;', '"'), '&#39;', ''''), '&amp;', '&'),
        'ǂ', ' ')), '\s+', ' ', 'g')), ' ', 'ǂ');
END
$$;
