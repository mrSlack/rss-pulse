// Command perfbench times store operations on a database filled by cmd/seed. It is a
// development tool for docs/2026-11-01-performance-baseline.md and modifies only rows it
// creates itself (they are deleted again).
//
//	go run ./cmd/perfbench -mode ingest -dsn ...   # CommitSourceRefresh with 200 items
//	go run ./cmd/perfbench -mode search -dsn ...   # ListArticlesForUser searches
//
// It uses only store APIs that also exist before stage 7, so the same file can be copied
// into an older checkout to measure the previous implementation.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

func main() {
	dsn := flag.String("dsn", "", "PostgreSQL connection string of a seeded database")
	mode := flag.String("mode", "ingest", "ingest or search")
	runs := flag.Int("runs", 7, "measured runs per scenario")
	sourceID := flag.Int64("source", 17, "seeded source refreshed by the ingest scenarios")
	userID := flag.Int64("user", 17, "seeded user whose articles are searched")
	queries := flag.String("q", "market,straße,безопасность,energy climate,ma,ent", "comma-separated search queries")
	flag.Parse()
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(*dsn)
	if err != nil {
		log.Fatal(err)
	}
	// One connection, so repeated searches reuse the same cached prepared statement
	// like a busy API connection does.
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	repository := store.New(pool)
	switch *mode {
	case "ingest":
		benchIngest(ctx, pool, repository, *sourceID, *runs)
	case "search":
		benchSearch(ctx, repository, *userID, strings.Split(*queries, ","), *runs)
	default:
		log.Fatalf("unknown -mode %q", *mode)
	}
}

func report(name string, values []time.Duration, extra string) {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	fmt.Printf("%-28s %s median %7.1f ms  runs", name, extra, milliseconds(sorted[len(sorted)/2]))
	for _, value := range sorted {
		fmt.Printf(" %.1f", milliseconds(value))
	}
	fmt.Println()
}

func milliseconds(value time.Duration) float64 {
	return float64(value.Microseconds()) / 1000
}

// benchIngest times three 200-item refreshes: an existing source whose items are all
// stored and unchanged, the same source with 20 new items, and a new source.
func benchIngest(ctx context.Context, pool *pgxpool.Pool, repository *store.Store, sourceID int64, runs int) {
	repository.SetArticleRetentionDays(180)
	rows, err := pool.Query(ctx, `SELECT guid,title,url,summary,published_at FROM articles WHERE feed_source_id=$1 ORDER BY published_at DESC LIMIT 200`, sourceID)
	if err != nil {
		log.Fatal(err)
	}
	known := make([]feeds.Item, 0, 200)
	for rows.Next() {
		var item feeds.Item
		if err := rows.Scan(&item.GUID, &item.Title, &item.URL, &item.Summary, &item.PublishedAt); err != nil {
			log.Fatal(err)
		}
		item.HasPublishedAt = true
		known = append(known, item)
	}
	rows.Close()
	if len(known) != 200 {
		log.Fatalf("source %d has %d articles, want at least 200", sourceID, len(known))
	}
	commit := func(id int64, items []feeds.Item) time.Duration {
		if _, err := pool.Exec(ctx, `UPDATE feed_sources SET lease_token='perfbench',lease_expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, id); err != nil {
			log.Fatal(err)
		}
		start := time.Now()
		if err := repository.CommitSourceRefresh(ctx, store.SourceLease{ID: id, Token: "perfbench"}, feeds.Document{Title: "Perfbench", Items: items}); err != nil {
			log.Fatal(err)
		}
		return time.Since(start)
	}
	// New items reuse stored summaries (realistic length) with distinct titles and URLs
	// so they are not merged into stored articles.
	fresh := func(run, count int) []feeds.Item {
		items := make([]feeds.Item, count)
		for i := range items {
			items[i] = feeds.Item{GUID: fmt.Sprintf("perfbench-%d-%d", run, i), Title: fmt.Sprintf("%s (perfbench %d)", known[i].Title, i),
				URL: fmt.Sprintf("https://perfbench.invalid/%d/%d", run, i), Summary: known[i].Summary, PublishedAt: known[i].PublishedAt, HasPublishedAt: true}
		}
		return items
	}

	commit(sourceID, known)
	values := make([]time.Duration, 0, runs)
	for range runs {
		values = append(values, commit(sourceID, known))
	}
	report("existing source, 200 known", values, "")

	values = values[:0]
	for run := 0; run <= runs; run++ {
		elapsed := commit(sourceID, append(fresh(run, 20), known[20:]...))
		if _, err := pool.Exec(ctx, `DELETE FROM articles WHERE feed_source_id=$1 AND guid LIKE 'perfbench-%'`, sourceID); err != nil {
			log.Fatal(err)
		}
		if run > 0 {
			values = append(values, elapsed)
		}
	}
	report("existing source, 20 new", values, "")

	values = values[:0]
	for run := 0; run <= runs; run++ {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ($1,'Perfbench',$1) RETURNING id`,
			fmt.Sprintf("https://perfbench.invalid/feed-%d-%d", time.Now().UnixNano(), run)).Scan(&id); err != nil {
			log.Fatal(err)
		}
		elapsed := commit(id, fresh(run, 200))
		if _, err := pool.Exec(ctx, `DELETE FROM feed_sources WHERE id=$1`, id); err != nil {
			log.Fatal(err)
		}
		if run > 0 {
			values = append(values, elapsed)
		}
	}
	report("new source, 200 new", values, "")
}

// benchSearch warms the statement cache with ten selective searches, then times each
// query on the same connection.
func benchSearch(ctx context.Context, repository *store.Store, userID int64, queries []string, runs int) {
	search := func(query string) (time.Duration, int) {
		start := time.Now()
		articles, err := repository.ListArticlesForUser(ctx, userID, "all", nil, nil, query, nil, 20, 0, nil)
		if err != nil {
			log.Fatal(err)
		}
		return time.Since(start), len(articles)
	}
	for range 10 {
		search("market")
	}
	for _, query := range queries {
		values := make([]time.Duration, 0, runs)
		count := 0
		for range runs {
			elapsed, rows := search(query)
			values = append(values, elapsed)
			count = rows
		}
		report(fmt.Sprintf("%q", query), values, fmt.Sprintf("rows %2d", count))
	}
}
