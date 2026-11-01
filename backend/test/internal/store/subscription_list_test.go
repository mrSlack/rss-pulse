package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/pulse-reader/app/internal/store"
)

func TestSubscriptionAndFolderNameListsAreScopedToTheUser(t *testing.T) {
	pool := isolatedPostgres(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	admin, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "long-bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, admin); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	var readerID, otherID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, role) VALUES ('reader@example.com', 'test-hash', 'Reader') RETURNING id`).Scan(&readerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, role) VALUES ('other@example.com', 'test-hash', 'Reader') RETURNING id`).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	tech, err := s.CreateFolderForUser(ctx, readerID, "tech", store.DefaultFolderColor)
	if err != nil {
		t.Fatal(err)
	}
	art, err := s.CreateFolderForUser(ctx, readerID, "Art", store.DefaultFolderColor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFolderForUser(ctx, otherID, "Other folder", store.DefaultFolderColor); err != nil {
		t.Fatal(err)
	}
	var first, second int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources(normalized_url,url,title) VALUES ('https://one.example/rss','https://one.example/rss','One') RETURNING id`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources(normalized_url,url,title) VALUES ('https://two.example/rss','https://two.example/rss','Two') RETURNING id`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id,folder_id,title_override,created_at) VALUES ($1,$2,NULL,'Personal one',NOW()-INTERVAL '1 hour'),($1,$3,$4,NULL,NOW()),($5,$2,NULL,NULL,NOW())`, readerID, first, second, tech.ID, otherID); err != nil {
		t.Fatal(err)
	}

	folders, err := s.ListFolderNamesForUser(ctx, readerID)
	if err != nil {
		t.Fatal(err)
	}
	wantFolders := []store.FolderName{{ID: art.ID, Name: "Art"}, {ID: tech.ID, Name: "tech"}}
	if !reflect.DeepEqual(folders, wantFolders) {
		t.Fatalf("ListFolderNamesForUser() = %+v, want %+v", folders, wantFolders)
	}

	subscriptions, err := s.ListSubscriptionsForUser(ctx, readerID)
	if err != nil {
		t.Fatal(err)
	}
	wantSubscriptions := []store.SubscriptionSummary{
		{Title: "Two", URL: "https://two.example/rss", FolderID: &tech.ID},
		{Title: "Personal one", URL: "https://one.example/rss"},
	}
	if !reflect.DeepEqual(subscriptions, wantSubscriptions) {
		t.Fatalf("ListSubscriptionsForUser() = %+v, want %+v", subscriptions, wantSubscriptions)
	}

	empty, err := s.ListFolderNamesForUser(ctx, -1)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("ListFolderNamesForUser(unknown) = %#v, %v; want empty slice", empty, err)
	}
}
