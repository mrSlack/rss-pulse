package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/store"
)

func smallConfig() Config {
	config := defaultConfig()
	config.Users = 4
	config.Sources = 6
	config.ArticlesPerSource = 20
	config.SubscriptionsPerUser = 4
	config.FoldersPerUser = 2
	config.QuarantineRecords = 5
	config.ConfirmDestroyData = true
	return config
}

func TestSeedRequiresConfirmation(t *testing.T) {
	config := smallConfig()
	config.ConfirmDestroyData = false
	if _, err := Seed(context.Background(), nil, config); !errors.Is(err, errNotConfirmed) {
		t.Fatalf("Seed() error = %v, want errNotConfirmed", err)
	}
}

func TestSeedRefusesDatabaseWithForeignAccounts(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureInitialAdministrator(ctx, pool, "owner@example.com", "a-long-bootstrap-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(ctx, pool, smallConfig()); !errors.Is(err, errForeignAccounts) {
		t.Fatalf("Seed() error = %v, want errForeignAccounts", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE email='owner@example.com'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("foreign account count = %d, err %v; the database must be untouched", count, err)
	}
}

func TestSeedFillsEmptyDatabaseAndCanBeRepeated(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	config := smallConfig()
	for run := 0; run < 2; run++ {
		if _, err := Seed(ctx, pool, config); err != nil {
			t.Fatalf("Seed() run %d error = %v", run+1, err)
		}
	}
	counts := map[string]int{}
	for _, table := range []string{"users", "feed_sources", "articles", "folders"} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	if counts["users"] != 4 || counts["feed_sources"] != 6 || counts["articles"] != 120 || counts["folders"] != 8 {
		t.Fatalf("counts = %v", counts)
	}
	var foreign int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE email NOT LIKE '%@seed.invalid'`).Scan(&foreign); err != nil || foreign != 0 {
		t.Fatalf("non-seed accounts = %d, err %v", foreign, err)
	}
	var states, orphanStates int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE NOT EXISTS (
		SELECT 1 FROM articles a JOIN user_feeds uf ON uf.feed_source_id=a.feed_source_id WHERE a.id=s.article_id AND uf.user_id=s.user_id))
		FROM user_article_states s`).Scan(&states, &orphanStates); err != nil {
		t.Fatal(err)
	}
	if states == 0 || orphanStates != 0 {
		t.Fatalf("states = %d, states outside subscriptions = %d", states, orphanStates)
	}
	repository := store.New(pool)
	var userID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE role='Reader' ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	user, err := repository.UserForSession(ctx, auth.HashSessionToken(SessionToken(userID)))
	if err != nil || user.ID != userID {
		t.Fatalf("seed session lookup = %+v, %v", user, err)
	}
	articles, err := repository.ListArticlesForUser(ctx, userID, "all", nil, nil, "", nil, 20, 0, nil)
	if err != nil || len(articles) == 0 {
		t.Fatalf("ListArticlesForUser() = %d articles, %v", len(articles), err)
	}
	var withTerm int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM articles WHERE STRPOS(LOWER(title || ' ' || summary), 'market') > 0`).Scan(&withTerm); err != nil {
		t.Fatal(err)
	}
	if withTerm > 20 {
		t.Fatalf("articles containing the search term = %d, want a small share", withTerm)
	}
	var withSearchText, mismatched int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE search_text LIKE '%market%'), COUNT(*) FILTER (WHERE search_text = '') FROM articles`).Scan(&withSearchText, &mismatched); err != nil {
		t.Fatal(err)
	}
	if withSearchText != withTerm || mismatched != 0 {
		t.Fatalf("search_text: %d matches (want %d), %d empty", withSearchText, withTerm, mismatched)
	}
	var nextID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://next.seed.invalid/rss','Next','https://next.seed.invalid/rss') RETURNING id`).Scan(&nextID); err != nil {
		t.Fatalf("sequence was not advanced past seeded ids: %v", err)
	}
}

func isolatedPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		if os.Getenv("REQUIRE_INTEGRATION") != "" {
			t.Fatal("REQUIRE_INTEGRATION is set but TEST_DATABASE_URL is empty")
		}
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL integration tests")
	}
	admin, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	schema := "test_seed_" + strings.ToLower(t.Name()) + "_" + time.Now().UTC().Format("150405000")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse test database URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		admin.Close()
		t.Fatalf("connect to isolated schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
		admin.Close()
	})
	return pool
}
