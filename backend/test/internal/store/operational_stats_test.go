package store_test

import (
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

func TestCommitSourceRefreshCountsInsertedAndQuarantinedArticles(t *testing.T) {
	f := newScheduleFixture(t, 20, 60)
	reader := f.user(t, "reader@example.com", nil)
	source := f.source(t, "counted", nil, nil, reader)
	if _, err := f.store.AddStopWord(f.ctx, "casino"); err != nil {
		t.Fatal(err)
	}
	published := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	document := feeds.Document{Title: "Counted", Items: []feeds.Item{
		{GUID: "a", Title: "First", URL: "https://counted.example/a", Summary: "One", PublishedAt: published, HasPublishedAt: true},
		{GUID: "b", Title: "Second", URL: "https://counted.example/b", Summary: "Two", PublishedAt: published.Add(time.Hour), HasPublishedAt: true},
		{GUID: "c", Title: "Casino bonus", URL: "https://counted.example/c", Summary: "Three", PublishedAt: published.Add(2 * time.Hour), HasPublishedAt: true},
	}}
	commit := func(token string) store.RefreshCounts {
		t.Helper()
		if _, err := f.pool.Exec(f.ctx, `UPDATE feed_sources SET lease_token=$2,lease_expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, source, token); err != nil {
			t.Fatal(err)
		}
		counts, err := f.store.CommitSourceRefresh(f.ctx, store.SourceLease{ID: source, Token: token}, document)
		if err != nil {
			t.Fatalf("CommitSourceRefresh() error = %v", err)
		}
		return counts
	}
	if got := commit("first"); got != (store.RefreshCounts{Inserted: 2, Quarantined: 1}) {
		t.Fatalf("first refresh counts = %+v, want 2 inserted and 1 quarantined", got)
	}
	// The same items again: updates and an already quarantined item count nothing.
	if got := commit("second"); got != (store.RefreshCounts{}) {
		t.Fatalf("repeated refresh counts = %+v, want zero", got)
	}
}

func TestOperationalStatsReportBacklogLeasesAndQueues(t *testing.T) {
	// TTL 20 minutes, global interval 60 minutes: a source checked 90 minutes ago
	// has been due for 30 minutes.
	f := newScheduleFixture(t, 20, 60)
	reader := f.user(t, "reader@example.com", nil)
	f.source(t, "never", nil, nil, reader)                            // due since creation (now): due, not overdue
	f.source(t, "late", minutes(90), minutes(90), reader)             // due for 30 minutes: overdue
	f.source(t, "fresh", minutes(10), minutes(10), reader)            // not due
	f.source(t, "unsubscribed", minutes(500), minutes(500))           // no subscriber: never due
	leased := f.source(t, "leased", minutes(10), minutes(10), reader) // not due, but being refreshed
	if _, err := f.pool.Exec(f.ctx, `UPDATE feed_sources SET lease_token='t',lease_expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, leased); err != nil {
		t.Fatal(err)
	}
	expired := f.source(t, "expired-lease", minutes(10), minutes(10), reader)
	if _, err := f.pool.Exec(f.ctx, `UPDATE feed_sources SET lease_token='t',lease_expires_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, expired); err != nil {
		t.Fatal(err)
	}
	var requestID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO feed_requests (normalized_url,url) VALUES ('https://requested.example/rss','https://requested.example/rss') RETURNING id`).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO feed_request_requesters (request_id,user_id) VALUES ($1,$2)`, requestID, reader); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"pending", "rejected", "allowed", "pending"} {
		if _, err := f.pool.Exec(f.ctx, `INSERT INTO blocked_feed_articles (feed_source_id,article_guid,article_title,feed_title,feed_url,matched_term,status,moderation_eligible,summary)
			VALUES ($1,gen_random_uuid()::text,'Title','Feed','https://leased.example/rss','spam',$2,TRUE,'Summary')`, leased, status); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := f.store.OperationalStats(f.ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("OperationalStats() error = %v", err)
	}
	if stats.SourcesDue != 2 || stats.SourcesOverdue != 1 {
		t.Fatalf("due=%d overdue=%d, want 2 and 1", stats.SourcesDue, stats.SourcesOverdue)
	}
	if stats.OldestOverdue < 29*time.Minute || stats.OldestOverdue > 31*time.Minute {
		t.Fatalf("oldest overdue = %v, want about 30 minutes", stats.OldestOverdue)
	}
	if stats.ActiveLeases != 1 {
		t.Fatalf("active leases = %d, want 1 (expired leases do not count)", stats.ActiveLeases)
	}
	if stats.PendingFeedRequests != 1 || stats.PendingQuarantine != 2 {
		t.Fatalf("pending requests=%d quarantine=%d, want 1 and 2", stats.PendingFeedRequests, stats.PendingQuarantine)
	}
}

func TestOperationalStatsOfAnEmptyDatabaseAreZero(t *testing.T) {
	f := newScheduleFixture(t, 20, 60)
	stats, err := f.store.OperationalStats(f.ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("OperationalStats() error = %v", err)
	}
	if stats != (store.OperationalStats{}) {
		t.Fatalf("stats = %+v, want zero", stats)
	}
}
