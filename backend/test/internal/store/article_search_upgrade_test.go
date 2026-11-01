package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

const (
	searchIndexName    = "articles_search_key_trgm_idx"
	oldSearchIndexName = "articles_search_text_trgm_idx"
)

// searchIndexState reports whether the named index exists in the test schema and is valid.
func searchIndexState(t *testing.T, f dedupFixture, name string) (exists, valid bool) {
	t.Helper()
	err := f.pool.QueryRow(context.Background(), `SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = $1 AND c.relnamespace = (SELECT relnamespace FROM pg_class WHERE oid = 'articles'::regclass)`, name).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true, valid
}

func nullSearchKeyCount(t *testing.T, f dedupFixture) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM articles WHERE search_key IS NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func runSearchUpgrade(t *testing.T, f dedupFixture) {
	t.Helper()
	if err := (store.ArticleSearchUpgrade{BatchSize: 2}).Run(context.Background(), f.pool); err != nil {
		t.Fatalf("ArticleSearchUpgrade.Run() error = %v", err)
	}
}

// assertSearchUsesIndex checks that the search key expression of the store's queries
// matches the index expression, so the planner can use the index for it.
func assertSearchUsesIndex(t *testing.T, f dedupFixture) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN SELECT a.id FROM articles a
		WHERE COALESCE(a.search_key, pulse_article_search_key(a.title, a.summary)) LIKE '%energyǂclimate%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), searchIndexName) {
		t.Fatalf("search key query does not use %s:\n%s", searchIndexName, plan.String())
	}
}

func TestArticleSearchFindsArticlesWithoutSearchKey(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)
	if _, err := f.pool.Exec(context.Background(), `UPDATE articles SET search_key = NULL`); err != nil {
		t.Fatal(err)
	}

	assertSearch(t, f, "МОСКВА", "ru")
	assertSearch(t, f, "растёт", "ru")
	assertSearch(t, f, "größere umleitung", "de")
	assertSearch(t, f, "link text", "markup")
	assertSearch(t, f, "& more", "markup")
	assertSearch(t, f, "href")
	assertSearch(t, f, "e", "de", "markup", "other")
}

func TestArticleSearchMatchesPhrasesAsSubstrings(t *testing.T) {
	f := newDedupFixture(t)
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	f.refresh(t,
		feeds.Item{GUID: "phrase", Title: "Synergy climatex policy", URL: "https://example.com/phrase", Summary: "energy climate change", PublishedAt: base, HasPublishedAt: true},
		feeds.Item{GUID: "split", Title: "Mar ket", URL: "https://example.com/split", Summary: "energyclimate", PublishedAt: base.Add(time.Hour), HasPublishedAt: true},
		feeds.Item{GUID: "apart", Title: "Energy and climate", URL: "https://example.com/apart", Summary: "climate energy", PublishedAt: base.Add(2 * time.Hour), HasPublishedAt: true},
	)
	for _, withoutSearchKey := range []bool{false, true} {
		if withoutSearchKey {
			if _, err := f.pool.Exec(context.Background(), `UPDATE articles SET search_key = NULL`); err != nil {
				t.Fatal(err)
			}
		}
		assertSearch(t, f, "energy climate", "phrase")
		assertSearch(t, f, "ergy clim", "phrase")
		assertSearch(t, f, "synergy climatex policy", "phrase")
		assertSearch(t, f, "y climatex p", "phrase")
		assertSearch(t, f, "climate energy", "apart")
		assertSearch(t, f, "energyclimate", "split")
		assertSearch(t, f, "market")
		assertSearch(t, f, "mar ket", "split")
		assertSearch(t, f, "climate change", "phrase")
		assertSearch(t, f, "a b")
	}
}

func TestArticleSearchUpgradeBackfillsAndBuildsIndex(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE articles SET search_key = NULL`); err != nil {
		t.Fatal(err)
	}
	if exists, _ := searchIndexState(t, f, searchIndexName); exists {
		t.Fatal("migrations must not build the search index; it is built after start")
	}

	runSearchUpgrade(t, f)

	if count := nullSearchKeyCount(t, f); count != 0 {
		t.Fatalf("%d articles without search_key after the backfill", count)
	}
	rows, err := f.pool.Query(ctx, `SELECT title, summary, search_key FROM articles`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var title, summary, searchKey string
		if err := rows.Scan(&title, &summary, &searchKey); err != nil {
			t.Fatal(err)
		}
		if want := store.ArticleSearchKey(title, summary); searchKey != want {
			t.Errorf("backfilled search_key = %q, want %q", searchKey, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if exists, valid := searchIndexState(t, f, searchIndexName); !exists || !valid {
		t.Fatalf("search index exists=%v valid=%v, want a valid index", exists, valid)
	}
	assertSearch(t, f, "größere umleitung", "de")
	assertSearch(t, f, "link text", "markup")
	assertSearchUsesIndex(t, f)

	// A completed upgrade is recorded and later runs return without touching rows.
	if _, err := f.pool.Exec(ctx, `UPDATE articles SET search_key = NULL WHERE guid = 'ru'`); err != nil {
		t.Fatal(err)
	}
	runSearchUpgrade(t, f)
	if count := nullSearchKeyCount(t, f); count != 1 {
		t.Fatalf("repeat run touched rows: %d without search_key, want 1", count)
	}
	assertSearch(t, f, "москва", "ru")
}

func TestArticleSearchUpgradeReplacesInvalidIndex(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)
	ctx := context.Background()
	// An interrupted CREATE INDEX CONCURRENTLY leaves an invalid index behind.
	if _, err := f.pool.Exec(ctx, `CREATE INDEX `+searchIndexName+` ON articles (id)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE pg_index SET indisvalid = FALSE WHERE indexrelid = '`+searchIndexName+`'::regclass`); err != nil {
		t.Fatalf("mark index invalid (the test database user must be a superuser): %v", err)
	}

	runSearchUpgrade(t, f)

	if exists, valid := searchIndexState(t, f, searchIndexName); !exists || !valid {
		t.Fatalf("search index exists=%v valid=%v, want a valid index", exists, valid)
	}
	var definition string
	if err := f.pool.QueryRow(ctx, `SELECT pg_get_indexdef('`+searchIndexName+`'::regclass)`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if want := "gin_trgm_ops"; !strings.Contains(definition, want) {
		t.Fatalf("index definition %q does not use %s", definition, want)
	}
}

// TestArticleSearchUpgradeFromFirstVersionOfMigration016 recreates a database that applied
// the first version of 016 (articles.search_text NOT NULL with a default, filled rows,
// trigram index on it) and 017, then starts the current version.
func TestArticleSearchUpgradeFromFirstVersionOfMigration016(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `DELETE FROM schema_migrations WHERE name LIKE '018_%';
		DROP FUNCTION pulse_article_search_key(TEXT, TEXT);
		ALTER TABLE articles DROP COLUMN search_key;
		ALTER TABLE articles ADD COLUMN search_text TEXT NOT NULL DEFAULT '';
		UPDATE articles SET search_text = lower(title);
		CREATE INDEX `+oldSearchIndexName+` ON articles USING gin (search_text public.gin_trgm_ops)`); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, f.pool, f.userID); err != nil {
		t.Fatalf("MigrateSharedSources() error = %v", err)
	}
	var oldColumn bool
	if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'articles' AND column_name = 'search_text')`).Scan(&oldColumn); err != nil {
		t.Fatal(err)
	}
	if oldColumn {
		t.Fatal("migration 018 kept the first version's search_text column")
	}
	if exists, _ := searchIndexState(t, f, oldSearchIndexName); exists {
		t.Fatal("the first version's trigram index was not dropped")
	}
	if count := nullSearchKeyCount(t, f); count != 4 {
		t.Fatalf("%d articles without search_key after the migrations, want all 4", count)
	}
	assertSearch(t, f, "größere umleitung", "de")

	runSearchUpgrade(t, f)

	if count := nullSearchKeyCount(t, f); count != 0 {
		t.Fatalf("%d articles without search_key after the backfill", count)
	}
	if exists, valid := searchIndexState(t, f, searchIndexName); !exists || !valid {
		t.Fatalf("search index exists=%v valid=%v, want a valid index", exists, valid)
	}
	assertSearch(t, f, "link text", "markup")
}

// TestArticleSearchUpgradeFromMigration015 recreates a database that stopped before 016
// (production ran migrations up to 010), so every existing article starts without
// search_key.
func TestArticleSearchUpgradeFromMigration015(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `DELETE FROM schema_migrations WHERE name LIKE '016_%' OR name LIKE '017_%' OR name LIKE '018_%';
		DROP FUNCTION pulse_article_search_key(TEXT, TEXT);
		ALTER TABLE articles DROP COLUMN search_key`); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, f.pool, f.userID); err != nil {
		t.Fatalf("MigrateSharedSources() error = %v", err)
	}
	if count := nullSearchKeyCount(t, f); count != 4 {
		t.Fatalf("%d articles without search_key after the migrations, want all 4", count)
	}
	assertSearch(t, f, "größere umleitung", "de")
	assertSearch(t, f, "e", "de", "markup", "other")

	runSearchUpgrade(t, f)

	if count := nullSearchKeyCount(t, f); count != 0 {
		t.Fatalf("%d articles without search_key after the backfill", count)
	}
	if exists, valid := searchIndexState(t, f, searchIndexName); !exists || !valid {
		t.Fatalf("search index exists=%v valid=%v, want a valid index", exists, valid)
	}
	assertSearch(t, f, "größere umleitung", "de")
}

func TestRefreshFillsMissingSearchKeyOfStoredArticles(t *testing.T) {
	f := newDedupFixture(t)
	items := searchFixtureItems()
	f.refresh(t, items...)
	if _, err := f.pool.Exec(context.Background(), `UPDATE articles SET search_key = NULL`); err != nil {
		t.Fatal(err)
	}
	f.refresh(t, items...)
	if count := nullSearchKeyCount(t, f); count != 0 {
		t.Fatalf("%d stored articles still without search_key after a refresh", count)
	}
	assertSearch(t, f, "größere umleitung", "de")
}

func TestArticleSearchTreatsSeparatorCharacterAsSpace(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, feeds.Item{GUID: "sep", Title: "Fooǂbar", URL: "https://example.com/sep", Summary: "<p>bazǂqux</p>", PublishedAt: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), HasPublishedAt: true})
	for _, withoutSearchKey := range []bool{false, true} {
		if withoutSearchKey {
			if _, err := f.pool.Exec(context.Background(), `UPDATE articles SET search_key = NULL`); err != nil {
				t.Fatal(err)
			}
		}
		assertSearch(t, f, "foo bar", "sep")
		assertSearch(t, f, "fooǂbar", "sep")
		assertSearch(t, f, "bar baz qux", "sep")
		assertSearch(t, f, "foobar")
	}
}

func TestArticleSearchUpgradeStopsWhenCanceled(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (store.ArticleSearchUpgrade{}).Run(ctx, f.pool); err == nil {
		t.Fatal("Run() with a canceled context returned no error")
	}
	runSearchUpgrade(t, f)
	if exists, valid := searchIndexState(t, f, searchIndexName); !exists || !valid {
		t.Fatalf("search index exists=%v valid=%v after a canceled and a complete run", exists, valid)
	}
}

func TestArticleSearchUpgradeReportsBackfillProgress(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)
	ctx := context.Background()
	command, err := f.pool.Exec(ctx, `UPDATE articles SET search_key = NULL`)
	if err != nil {
		t.Fatal(err)
	}
	var reported []int64
	upgrade := store.ArticleSearchUpgrade{BatchSize: 2, Progress: func(filled int64) { reported = append(reported, filled) }}
	if err := upgrade.Run(ctx, f.pool); err != nil {
		t.Fatalf("ArticleSearchUpgrade.Run() error = %v", err)
	}
	var total int64
	for _, filled := range reported {
		total += filled
	}
	if total != command.RowsAffected() || len(reported) < 2 {
		t.Fatalf("progress reports %v (sum %d), want one per batch summing to %d", reported, total, command.RowsAffected())
	}
}
