package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// DefaultArticleSearchBatchSize is the number of articles one backfill statement
	// visits; each batch is its own short transaction.
	DefaultArticleSearchBatchSize = 1000
	articleSearchIndex            = "articles_search_key_trgm_idx"
	// articleSearchUpgradeMarker is recorded in schema_migrations once every step has
	// completed, so later starts skip the upgrade.
	articleSearchUpgradeMarker = "018_article_search_key.sql:background"
	articleSearchUpgradeLock   = 2026110118
)

// ArticleSearchUpgrade completes the article search schema of migration 018 while the
// API is already serving: it fills articles.search_key for rows without it in batches,
// builds the trigram index articles_search_key_trgm_idx with CREATE INDEX CONCURRENTLY,
// and analyzes the table. Search is correct at every step (see articleSearchKey); only
// its speed depends on the progress. Run is idempotent and resumes after an
// interruption; concurrent runs for the same schema wait for each other through a
// session advisory lock.
type ArticleSearchUpgrade struct {
	// BatchSize is the number of articles per backfill statement
	// (DefaultArticleSearchBatchSize when zero or negative).
	BatchSize int
	// Pause is waited between backfill batches to leave room for other work.
	Pause time.Duration
	// Logger receives progress records; nil discards them.
	Logger *slog.Logger
	// Progress, when set, is called after each backfill batch with the number of
	// articles that batch filled.
	Progress func(filled int64)
}

// Run performs the remaining steps of the upgrade and returns nil once all of them have
// completed, immediately when an earlier run completed them.
func (u ArticleSearchUpgrade) Run(ctx context.Context, pool *pgxpool.Pool) error {
	logger := u.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	batchSize := u.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultArticleSearchBatchSize
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// A session-level lock: CREATE INDEX CONCURRENTLY cannot run inside a transaction.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1, hashtext(current_schema()))`, articleSearchUpgradeLock); err != nil {
		return fmt.Errorf("lock article search upgrade: %w", err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1, hashtext(current_schema()))`, articleSearchUpgradeLock); err != nil {
			// Closing the connection releases the lock; the pool discards a closed connection.
			_ = conn.Conn().Close(context.Background())
		}
	}()

	var completed bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)`, articleSearchUpgradeMarker).Scan(&completed); err != nil {
		return err
	}
	if completed {
		return nil
	}
	started := time.Now()
	logger.Info("article search upgrade started")
	filled, err := u.backfill(ctx, conn, batchSize, logger)
	if err != nil {
		return fmt.Errorf("backfill article search keys: %w", err)
	}
	logger.Info("article search keys backfilled", slog.Int64("articles", filled), slog.Duration("duration", time.Since(started)))
	if err := buildArticleSearchIndex(ctx, conn, logger); err != nil {
		return fmt.Errorf("build article search index: %w", err)
	}
	// The planner estimates search selectivity from the statistics of the index expression.
	if _, err := conn.Exec(ctx, `ANALYZE articles`); err != nil {
		return fmt.Errorf("analyze articles: %w", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`, articleSearchUpgradeMarker); err != nil {
		return err
	}
	logger.Info("article search upgrade completed", slog.Duration("duration", time.Since(started)))
	return nil
}

// backfill fills search_key in id order, batch by batch. Rows that another transaction
// has locked are skipped: ingestion, the only writer of article text, sets search_key
// itself, and a row left without it is still found through articleSearchKey.
func (u ArticleSearchUpgrade) backfill(ctx context.Context, conn *pgxpool.Conn, batchSize int, logger *slog.Logger) (int64, error) {
	var lastID, total int64
	lastLogged := time.Now()
	for {
		var batchEnd *int64
		var filled int64
		err := conn.QueryRow(ctx, `WITH batch AS (
				SELECT id FROM articles WHERE id > $1 ORDER BY id LIMIT $2
			), pending AS (
				SELECT a.id FROM articles a JOIN batch ON batch.id = a.id
				WHERE a.search_key IS NULL FOR UPDATE OF a SKIP LOCKED
			), filled AS (
				UPDATE articles a SET search_key = pulse_article_search_key(a.title, a.summary)
				FROM pending WHERE a.id = pending.id RETURNING 1
			)
			SELECT (SELECT MAX(id) FROM batch), (SELECT COUNT(*) FROM filled)`, lastID, batchSize).Scan(&batchEnd, &filled)
		if err != nil {
			return total, err
		}
		if batchEnd == nil {
			return total, nil
		}
		lastID = *batchEnd
		total += filled
		if u.Progress != nil {
			u.Progress(filled)
		}
		if time.Since(lastLogged) >= 30*time.Second {
			logger.Info("article search key backfill progress", slog.Int64("filled", total), slog.Int64("last_article_id", lastID))
			lastLogged = time.Now()
		}
		if u.Pause > 0 {
			timer := time.NewTimer(u.Pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return total, ctx.Err()
			case <-timer.C:
			}
		}
	}
}

// buildArticleSearchIndex creates the trigram index unless a valid one exists. An
// interrupted CREATE INDEX CONCURRENTLY leaves an invalid index, which is dropped first.
func buildArticleSearchIndex(ctx context.Context, conn *pgxpool.Conn, logger *slog.Logger) error {
	var valid bool
	err := conn.QueryRow(ctx, `SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = $1 AND c.relnamespace = (SELECT relnamespace FROM pg_class WHERE oid = 'articles'::regclass)`, articleSearchIndex).Scan(&valid)
	switch {
	case err == nil && valid:
		return nil
	case err == nil:
		logger.Warn("dropping invalid article search index left by an interrupted build")
		if _, err := conn.Exec(ctx, `DROP INDEX CONCURRENTLY IF EXISTS `+articleSearchIndex); err != nil {
			return err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	var trigramSchema string
	if err := conn.QueryRow(ctx, `SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'pg_trgm'`).Scan(&trigramSchema); err != nil {
		return fmt.Errorf("find pg_trgm: %w", err)
	}
	// A larger sort memory builds the index about a third faster on 500,000 articles.
	if _, err := conn.Exec(ctx, `SET maintenance_work_mem = '256MB'`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `RESET maintenance_work_mem`)
	started := time.Now()
	logger.Info("building article search index")
	operatorClass := pgx.Identifier{trigramSchema, "gin_trgm_ops"}.Sanitize()
	if _, err := conn.Exec(ctx, `CREATE INDEX CONCURRENTLY `+articleSearchIndex+` ON articles USING gin (`+searchKeyExpression("")+` `+operatorClass+`)`); err != nil {
		return err
	}
	logger.Info("article search index built", slog.Duration("duration", time.Since(started)))
	return nil
}
