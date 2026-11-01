package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pulse-reader/app/internal/feeds"
)

// duplicateWindow is how far apart the publication times of an article stored under
// one GUID and an item with an unknown GUID may be for the item to be merged into it.
const duplicateWindow = 30 * time.Minute

type ingestArticle struct {
	id        int64 // 0 while the row is only queued for insertion
	guid      string
	title     string
	url       string
	summary   string
	published time.Time
	searchKey string
}

func (a ingestArticle) equal(b ingestArticle) bool {
	return a.id == b.id && a.guid == b.guid && a.title == b.title && a.url == b.url && a.summary == b.summary &&
		a.published.Equal(b.published) && a.searchKey == b.searchKey
}

// upsertSourceItems stores a refresh's items in feed order. For each item:
//
//  1. An article of the source with the same GUID is updated (its publication time only
//     when the item is dated).
//  2. Otherwise an article republished under a new GUID is merged: it must lie within
//     30 minutes and have an equal non-empty URL, or both an equal non-empty title and an
//     equal non-empty summary. Repeated titles ("Daily digest") or boilerplate summaries
//     alone must not merge distinct articles and their state. A URL match is preferred,
//     then the closest publication time.
//  3. Otherwise the item is inserted.
//
// Later items see the effects of earlier ones. Instead of two or three statements per
// item, the function loads every article that any item could match in one query (the
// articles with an item's GUID and those satisfying an item's merge rule with their stored
// values; no other article is changed by the refresh, so none can match later), applies
// the rules above in memory, and sends only the resulting writes, in order, as one batch.
// Writes that would not change a row are skipped. It returns the number of inserted
// articles.
func upsertSourceItems(ctx context.Context, tx pgx.Tx, sourceID int64, items []feeds.Item) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	known, err := loadIngestCandidates(ctx, tx, sourceID, items)
	if err != nil {
		return 0, err
	}
	inserted := 0
	byGUID := make(map[string]*ingestArticle, len(known)+len(items))
	for _, article := range known {
		byGUID[article.guid] = article
	}
	batch := &pgx.Batch{}
	for _, item := range items {
		// pgx sends timestamptz values with microsecond precision, truncating the rest.
		published := item.PublishedAt.Truncate(time.Microsecond)
		next := ingestArticle{guid: item.GUID, title: item.Title, url: item.URL, summary: item.Summary,
			published: published, searchKey: ArticleSearchKey(item.Title, item.Summary)}

		target := byGUID[item.GUID]
		if target == nil {
			target = closestDuplicate(known, item, published)
		}
		if target == nil {
			article := next
			known = append(known, &article)
			byGUID[article.guid] = &article
			batch.Queue(`INSERT INTO articles (feed_source_id, guid, title, url, summary, published_at, search_key)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`, sourceID, article.guid, article.title, article.url, article.summary, article.published, article.searchKey)
			inserted++
			continue
		}
		if !item.HasPublishedAt {
			next.published = target.published
		}
		next.id = target.id
		if next.equal(*target) {
			continue
		}
		previousGUID := target.guid
		if target.id != 0 {
			batch.Queue(`UPDATE articles SET guid=$2, title=$3, url=$4, summary=$5, published_at=$6, search_key=$7 WHERE id=$1`,
				target.id, next.guid, next.title, next.url, next.summary, next.published, next.searchKey)
		} else {
			batch.Queue(`UPDATE articles SET guid=$3, title=$4, url=$5, summary=$6, published_at=$7, search_key=$8 WHERE feed_source_id=$1 AND guid=$2`,
				sourceID, previousGUID, next.guid, next.title, next.url, next.summary, next.published, next.searchKey)
		}
		*target = next
		if previousGUID != next.guid {
			delete(byGUID, previousGUID)
			byGUID[next.guid] = target
		}
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	results := tx.SendBatch(ctx, batch)
	for range batch.Len() {
		if _, err := results.Exec(); err != nil {
			results.Close()
			return 0, err
		}
	}
	if err := results.Close(); err != nil {
		return 0, err
	}
	return inserted, nil
}

// loadIngestCandidates locks and returns, ordered by id, the source's articles that share
// an item's GUID or satisfy an item's merge rule.
func loadIngestCandidates(ctx context.Context, tx pgx.Tx, sourceID int64, items []feeds.Item) ([]*ingestArticle, error) {
	guids := make([]string, len(items))
	titles := make([]string, len(items))
	urls := make([]string, len(items))
	summaries := make([]string, len(items))
	published := make([]time.Time, len(items))
	for i, item := range items {
		guids[i], titles[i], urls[i], summaries[i], published[i] = item.GUID, item.Title, item.URL, item.Summary, item.PublishedAt
	}
	rows, err := tx.Query(ctx, `SELECT a.id, a.guid, a.title, a.url, a.summary, a.published_at, COALESCE(a.search_key, '')
		FROM articles a
		WHERE a.feed_source_id = $1 AND a.id IN (
			SELECT id FROM articles WHERE feed_source_id = $1 AND guid = ANY($2::text[])
			UNION
			SELECT candidate.id
			FROM unnest($3::text[], $4::text[], $5::text[], $6::timestamptz[]) AS item(title, url, summary, published_at)
			JOIN articles candidate ON candidate.feed_source_id = $1
				AND candidate.published_at BETWEEN item.published_at - INTERVAL '30 minutes' AND item.published_at + INTERVAL '30 minutes'
				AND ((item.url <> '' AND candidate.url = item.url)
					OR (item.title <> '' AND item.summary <> '' AND candidate.title = item.title AND candidate.summary = item.summary))
		)
		ORDER BY a.id
		FOR UPDATE`, sourceID, guids, titles, urls, summaries, published)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]*ingestArticle, 0)
	for rows.Next() {
		var article ingestArticle
		if err := rows.Scan(&article.id, &article.guid, &article.title, &article.url, &article.summary, &article.published, &article.searchKey); err != nil {
			return nil, err
		}
		result = append(result, &article)
	}
	return result, rows.Err()
}

// closestDuplicate applies the merge rule of upsertSourceItems to the known articles,
// which are ordered by id (rows queued for insertion last, in insertion order); among
// equally good matches the first one wins.
func closestDuplicate(known []*ingestArticle, item feeds.Item, published time.Time) *ingestArticle {
	var best *ingestArticle
	bestURLMatch := false
	var bestDistance time.Duration
	for _, article := range known {
		distance := article.published.Sub(published)
		if distance < -duplicateWindow || distance > duplicateWindow {
			continue
		}
		if distance < 0 {
			distance = -distance
		}
		urlMatch := item.URL != "" && article.url == item.URL
		if !urlMatch && (item.Title == "" || item.Summary == "" || article.title != item.Title || article.summary != item.Summary) {
			continue
		}
		if best == nil || (urlMatch && !bestURLMatch) || (urlMatch == bestURLMatch && distance < bestDistance) {
			best, bestURLMatch, bestDistance = article, urlMatch, distance
		}
	}
	return best
}
