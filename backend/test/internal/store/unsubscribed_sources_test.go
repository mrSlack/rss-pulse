package store_test

import (
	"context"
	"testing"
)

// unsubscribedSource inserts a source with articles and, when orphanedDaysAgo >= 0,
// marks it unsubscribed since that many days.
func (f retentionFixture) unsubscribedSource(t *testing.T, url string, orphanedDaysAgo, articles int) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ($1,$1,$1) RETURNING id`, url).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if orphanedDaysAgo >= 0 {
		if _, err := f.pool.Exec(ctx, `UPDATE feed_sources SET unsubscribed_since=NOW()-make_interval(days=>$2) WHERE id=$1`, id, orphanedDaysAgo); err != nil {
			t.Fatal(err)
		}
	}
	if articles > 0 {
		if _, err := f.pool.Exec(ctx, `INSERT INTO articles (feed_source_id,guid,title)
			SELECT $1, 'guid-'||n, 'Article '||n FROM generate_series(1,$2) n`, id, articles); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (f retentionFixture) sourceExists(t *testing.T, id int64) bool {
	t.Helper()
	return f.count(t, `SELECT COUNT(*) FROM feed_sources WHERE id=$1`, id) == 1
}

func TestDeleteUnsubscribedSourcesRemovesExpiredSourcesInBatches(t *testing.T) {
	ctx := context.Background()
	f := newRetentionFixture(t)
	expired := f.unsubscribedSource(t, "https://expired.example/rss", 40, 5)
	// A former subscriber's read and saved states do not keep the source.
	if _, err := f.pool.Exec(ctx, `INSERT INTO user_article_states (user_id,article_id,is_read,is_saved)
		SELECT $1, id, TRUE, TRUE FROM articles WHERE feed_source_id=$2`, f.readerID, expired); err != nil {
		t.Fatal(err)
	}
	// Decided quarantine history of the source is removed with it.
	if _, err := f.pool.Exec(ctx, `INSERT INTO blocked_feed_articles (feed_source_id,article_guid,article_title,feed_title,feed_url,matched_term,status,decided_at,moderation_eligible,summary)
		VALUES ($1,'blocked','Blocked','Expired','https://expired.example/rss','spam','rejected',NOW(),TRUE,'')`, expired); err != nil {
		t.Fatal(err)
	}
	subscribedArticles := f.count(t, `SELECT COUNT(*) FROM articles WHERE feed_source_id=$1`, f.sourceID)
	f.insertArticle(t, "subscribed", 1, 1)

	sources, articles, err := f.store.DeleteUnsubscribedSources(ctx, 30, 2)
	if err != nil {
		t.Fatalf("DeleteUnsubscribedSources() error = %v", err)
	}
	if sources != 1 || articles != 5 {
		t.Fatalf("deleted %d sources and %d articles, want 1 and 5", sources, articles)
	}
	if f.sourceExists(t, expired) {
		t.Fatal("expired unsubscribed source survived")
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM articles WHERE feed_source_id=$1`:                                                     0,
		`SELECT COUNT(*) FROM blocked_feed_articles WHERE feed_source_id=$1`:                                        0,
		`SELECT COUNT(*) FROM user_article_states s JOIN articles a ON a.id=s.article_id WHERE a.feed_source_id=$1`: 0,
	} {
		if got := f.count(t, query, expired); got != want {
			t.Fatalf("%s = %d, want %d", query, got, want)
		}
	}
	if got := f.count(t, `SELECT COUNT(*) FROM user_article_states`); got != 0 {
		t.Fatalf("user_article_states = %d, want the deleted source's states removed", got)
	}
	if !f.sourceExists(t, f.sourceID) || f.count(t, `SELECT COUNT(*) FROM articles WHERE feed_source_id=$1`, f.sourceID) != subscribedArticles+1 {
		t.Fatal("subscribed source or its articles were deleted")
	}
}

func TestDeleteUnsubscribedSourcesKeepsReferencedAndRecentSources(t *testing.T) {
	ctx := context.Background()
	f := newRetentionFixture(t)
	recent := f.unsubscribedSource(t, "https://recent.example/rss", 10, 1)
	requested := f.unsubscribedSource(t, "https://requested.example/rss", 40, 1)
	var requestID int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO feed_requests (normalized_url,url) VALUES ('https://requested.example/rss','https://requested.example/rss') RETURNING id`).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO feed_request_requesters (request_id,user_id) VALUES ($1,$2)`, requestID, f.readerID); err != nil {
		t.Fatal(err)
	}
	quarantined := f.unsubscribedSource(t, "https://quarantined.example/rss", 40, 1)
	if _, err := f.pool.Exec(ctx, `INSERT INTO blocked_feed_articles (feed_source_id,article_guid,article_title,feed_title,feed_url,matched_term,status,moderation_eligible,summary)
		VALUES ($1,'pending','Pending','Quarantined','https://quarantined.example/rss','spam','pending',TRUE,'Summary')`, quarantined); err != nil {
		t.Fatal(err)
	}
	leased := f.unsubscribedSource(t, "https://leased.example/rss", 40, 1)
	if _, err := f.pool.Exec(ctx, `UPDATE feed_sources SET lease_token='t', lease_expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, leased); err != nil {
		t.Fatal(err)
	}
	// A subscription of a disabled account still counts: re-enabling restores it.
	disabledFollower := f.unsubscribedSource(t, "https://disabled.example/rss", 40, 1)
	if _, err := f.pool.Exec(ctx, `UPDATE users SET disabled_at=NOW() WHERE id=$1`, f.readerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, f.readerID, disabledFollower); err != nil {
		t.Fatal(err)
	}
	neverMarked := f.unsubscribedSource(t, "https://unmarked.example/rss", -1, 1)

	sources, articles, err := f.store.DeleteUnsubscribedSources(ctx, 30, 5000)
	if err != nil {
		t.Fatalf("DeleteUnsubscribedSources() error = %v", err)
	}
	if sources != 0 || articles != 0 {
		t.Fatalf("deleted %d sources and %d articles, want none", sources, articles)
	}
	for name, id := range map[string]int64{"recent": recent, "requested": requested, "quarantined": quarantined, "leased": leased, "disabled follower": disabledFollower, "never marked": neverMarked} {
		if !f.sourceExists(t, id) || f.count(t, `SELECT COUNT(*) FROM articles WHERE feed_source_id=$1`, id) != 1 {
			t.Fatalf("%s source or its article was deleted", name)
		}
	}
	// The run starts the grace period of a source it sees without subscribers for
	// the first time and ends it for a source that has subscribers again.
	if got := f.count(t, `SELECT COUNT(*) FROM feed_sources WHERE id=$1 AND unsubscribed_since > NOW()-INTERVAL '1 minute'`, neverMarked); got != 1 {
		t.Fatal("source without subscribers was not marked as unsubscribed")
	}
	if got := f.count(t, `SELECT COUNT(*) FROM feed_sources WHERE id IN ($1,$2) AND unsubscribed_since IS NULL`, disabledFollower, f.sourceID); got != 2 {
		t.Fatal("subscribed sources must not be marked as unsubscribed")
	}

	if sources, articles, err := f.store.DeleteUnsubscribedSources(ctx, 0, 5000); err != nil || sources != 0 || articles != 0 {
		t.Fatalf("disabled cleanup deleted %d sources and %d articles, err %v", sources, articles, err)
	}
}

func TestResubscribingEndsTheUnsubscribedGracePeriod(t *testing.T) {
	ctx := context.Background()
	f := newRetentionFixture(t)
	reused := f.unsubscribedSource(t, "https://reused.example/rss", 40, 1)
	// Adding an existing source counts as approved and must stop its deletion before
	// the subscription itself is created in a separate transaction.
	result, err := f.store.RequestFeedApproval(ctx, f.readerID, "https://reused.example/rss", "", nil, nil)
	if err != nil || !result.Approved || result.SourceID != reused {
		t.Fatalf("RequestFeedApproval() = %+v, %v; want the existing source approved", result, err)
	}
	approved := f.unsubscribedSource(t, "https://approved.example/rss", 40, 1)
	var requestID int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO feed_requests (normalized_url,url) VALUES ('https://approved.example/rss','https://approved.example/rss') RETURNING id`).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO feed_request_requesters (request_id,user_id,replace_feed_id)
		VALUES ($1,$2,(SELECT id FROM user_feeds WHERE user_id=$2 AND feed_source_id=$3))`, requestID, f.readerID, f.sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ApproveFeedRequest(ctx, requestID); err != nil {
		t.Fatal(err)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM feed_sources WHERE id IN ($1,$2) AND unsubscribed_since IS NULL`, reused, approved); got != 2 {
		t.Fatalf("sources with a cleared grace period = %d, want 2", got)
	}
	if sources, _, err := f.store.DeleteUnsubscribedSources(ctx, 30, 5000); err != nil || sources != 0 {
		t.Fatalf("DeleteUnsubscribedSources() deleted %d sources, err %v; want none", sources, err)
	}
	if !f.sourceExists(t, reused) || !f.sourceExists(t, approved) {
		t.Fatal("a reused source was deleted")
	}
}
