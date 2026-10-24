package store_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/store"
)

func TestInitialAdministratorBootstrapIsRepeatSafe(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if _, err := store.EnsureInitialAdministrator(ctx, pool, "", ""); err != store.ErrBootstrapRequired {
		t.Fatalf("EnsureInitialAdministrator() error = %v, want ErrBootstrapRequired", err)
	}
	firstID, err := store.EnsureInitialAdministrator(ctx, pool, "Admin@Example.com", "a-long-bootstrap-password")
	if err != nil {
		t.Fatalf("EnsureInitialAdministrator() error = %v", err)
	}
	secondID, err := store.EnsureInitialAdministrator(ctx, pool, "ignored@example.com", "a-different-password")
	if err != nil {
		t.Fatalf("repeat EnsureInitialAdministrator() error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("repeat bootstrap id = %d, want original id %d", secondID, firstID)
	}
	var count int
	var email string
	if err := pool.QueryRow(ctx, `SELECT COUNT(*), MIN(email) FROM users`).Scan(&count, &email); err != nil {
		t.Fatal(err)
	}
	if count != 1 || email != "admin@example.com" {
		t.Fatalf("users = (%d, %q), want one normalized bootstrap administrator", count, email)
	}
	var ownedFolders int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM folders WHERE owner_user_id = $1`, firstID).Scan(&ownedFolders); err != nil {
		t.Fatal(err)
	}
	if ownedFolders == 0 {
		t.Fatal("initial folders were not assigned to the bootstrap administrator")
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("repeat Migrate() error = %v", err)
	}
	if _, err := store.EnsureInitialAdministrator(ctx, pool, "", ""); err != nil {
		t.Fatalf("existing database must not require bootstrap credentials: %v", err)
	}
}

func isolatedPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL integration tests")
	}
	admin, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	schema := "test_" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_") + "_" + time.Now().UTC().Format("150405000")
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
