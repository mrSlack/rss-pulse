package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/feeds"
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	ErrNotFound               = errors.New("record not found")
	ErrDuplicate              = errors.New("an object with that name or URL already exists")
	ErrInvalidFeedURL         = errors.New("feed URL must be an absolute HTTP or HTTPS URL")
	ErrBootstrapRequired      = errors.New("INITIAL_ADMIN_EMAIL and INITIAL_ADMIN_PASSWORD are required because the database has no users")
	ErrLeaseLost              = errors.New("refresh lease is no longer owned by this worker")
	ErrFolderUnavailable      = errors.New("selected folder is unavailable")
	ErrInvalidFolderOperation = errors.New("invalid folder deletion operation")
	ErrFeedLimitReached       = errors.New("the user has reached the feed subscription limit")
	ErrLastAdministrator      = errors.New("cannot demote the last Administrator")
)

const (
	DefaultFolderColor = "amber"
	MaxFeedsPerUser    = 100
)

func IsValidFolderColor(color string) bool {
	switch color {
	case "amber", "blue", "green", "rose", "violet", "teal":
		return true
	}
	if len(color) != 7 || color[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(color[1:], 16, 24)
	return err == nil
}

type Store struct {
	pool *pgxpool.Pool
}

type Folder struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	UnreadCount int    `json:"unreadCount"`
}

type Feed struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	URL           string     `json:"url"`
	FolderID      *int64     `json:"folderId"`
	FolderName    *string    `json:"folderName"`
	UnreadCount   int        `json:"unreadCount"`
	LastCheckedAt *time.Time `json:"lastCheckedAt"`
	LastError     *string    `json:"lastError"`
}

type Article struct {
	ID          int64     `json:"id"`
	FeedID      int64     `json:"feedId"`
	FeedTitle   string    `json:"feedTitle"`
	FeedURL     string    `json:"feedUrl"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Summary     string    `json:"summary"`
	PublishedAt time.Time `json:"publishedAt"`
	IsRead      bool      `json:"isRead"`
	IsSaved     bool      `json:"isSaved"`
}

type FeedTarget struct {
	ID  int64
	URL string
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := applyMigrationPhase(ctx, pool, "core", "001_initial.sql", nil); err != nil {
		return err
	}
	return applyMigrationPhase(ctx, pool, "accounts", "002_accounts.sql", nil)
}

func EnsureInitialAdministrator(ctx context.Context, pool *pgxpool.Pool, email, password string) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2026102401)`); err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, err
	}
	var id int64
	if count > 0 {
		if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE role = 'Administrator' ORDER BY id LIMIT 1`).Scan(&id); err != nil {
			return 0, errors.New("database has users but no Administrator account")
		}
	} else {
		if strings.TrimSpace(email) == "" || password == "" {
			return 0, ErrBootstrapRequired
		}
		email = strings.ToLower(strings.TrimSpace(email))
		if !validEmail(email) || len([]rune(password)) < 12 {
			return 0, errors.New("initial administrator email must be valid and password must contain at least 12 characters")
		}
		passwordHash, err := auth.HashPassword(password)
		if err != nil {
			return 0, err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO users (email, password_hash, role) VALUES ($1, $2, 'Administrator') RETURNING id`, email, passwordHash).Scan(&id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE folders SET owner_user_id = $1 WHERE owner_user_id IS NULL`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

const consolidatedMigrationFile = "001_pulse.sql"

func migrationPhases() (map[string]string, error) {
	contents, err := migrations.ReadFile("migrations/" + consolidatedMigrationFile)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(contents), "-- pulse:phase ")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) != "" {
		return nil, errors.New("consolidated migration has an invalid phase header")
	}
	phases := make(map[string]string, len(parts)-1)
	for _, part := range parts[1:] {
		lineEnd := strings.IndexByte(part, '\n')
		if lineEnd < 0 {
			return nil, errors.New("consolidated migration phase has no SQL body")
		}
		name := strings.TrimSpace(part[:lineEnd])
		body := strings.TrimSpace(part[lineEnd+1:])
		if name == "" || body == "" {
			return nil, errors.New("consolidated migration contains an empty phase")
		}
		if _, exists := phases[name]; exists {
			return nil, fmt.Errorf("consolidated migration repeats phase %q", name)
		}
		phases[name] = body
	}
	return phases, nil
}

func applyMigrationPhase(ctx context.Context, pool *pgxpool.Pool, phase, legacyName string, afterSQL func(context.Context, pgx.Tx) error) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	phases, err := migrationPhases()
	if err != nil {
		return err
	}
	schema, ok := phases[phase]
	if !ok {
		return fmt.Errorf("consolidated migration is missing phase %q", phase)
	}
	marker := consolidatedMigrationFile + ":" + phase
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2026102402)`); err != nil {
		return err
	}
	var applied bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1 OR name = $2)`, marker, legacyName).Scan(&applied); err != nil {
		return err
	}
	if !applied {
		if _, err = tx.Exec(ctx, schema); err == nil && afterSQL != nil {
			err = afterSQL(ctx, tx)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, marker)
		}
	}
	if err != nil {
		return fmt.Errorf("apply migration phase %s: %w", phase, err)
	}
	return tx.Commit(ctx)
}

func applyMigrationFile(ctx context.Context, pool *pgxpool.Pool, fileName string, legacyNames ...string) error {
	contents, err := migrations.ReadFile("migrations/" + fileName)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", fileName, err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2026102402)`); err != nil {
		return err
	}
	markers := append([]string{fileName}, legacyNames...)
	var applied bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = ANY($1::text[]))`, markers).Scan(&applied); err != nil {
		return err
	}
	if applied {
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`, fileName); err != nil {
			return fmt.Errorf("record migration %s: %w", fileName, err)
		}
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, string(contents)); err != nil {
		return fmt.Errorf("apply migration %s: %w", fileName, err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, fileName); err != nil {
		return fmt.Errorf("record migration %s: %w", fileName, err)
	}
	return tx.Commit(ctx)
}

func applyMigrationFiles(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("list migration files: %w", err)
	}
	fileNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") && entry.Name() != consolidatedMigrationFile {
			fileNames = append(fileNames, entry.Name())
		}
	}
	sort.Strings(fileNames)
	legacyMarkers := map[string][]string{
		"006_folder_customization.sql": {"001_pulse.sql:folder_customization"},
	}
	for _, fileName := range fileNames {
		if err := applyMigrationFile(ctx, pool, fileName, legacyMarkers[fileName]...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.name, d.color, COUNT(a.id) FILTER (WHERE a.is_read = FALSE)::int
		FROM folders d
		LEFT JOIN feeds f ON f.folder_id = d.id
		LEFT JOIN articles a ON a.feed_id = f.id
		GROUP BY d.id
		ORDER BY LOWER(d.name), d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	folders := make([]Folder, 0)
	for rows.Next() {
		var folder Folder
		if err := rows.Scan(&folder.ID, &folder.Name, &folder.Color, &folder.UnreadCount); err != nil {
			return nil, err
		}
		folders = append(folders, folder)
	}
	return folders, rows.Err()
}

func (s *Store) CreateFolder(ctx context.Context, name string) (Folder, error) {
	var folder Folder
	err := s.pool.QueryRow(ctx, `INSERT INTO folders (name) VALUES ($1) ON CONFLICT DO NOTHING RETURNING id, name`, name).Scan(&folder.ID, &folder.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Folder{}, ErrDuplicate
	}
	return folder, err
}

func (s *Store) ListFeeds(ctx context.Context) ([]Feed, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.title, f.url, f.folder_id, d.name,
		       COUNT(a.id) FILTER (WHERE a.is_read = FALSE)::int,
		       f.last_checked_at, f.last_error
		FROM feeds f
	LEFT JOIN folders d ON d.id = f.folder_id
	LEFT JOIN articles a ON a.feed_id = f.id
	GROUP BY f.id, d.name
	ORDER BY f.created_at DESC, f.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Feed, 0)
	for rows.Next() {
		var feed Feed
		if err := rows.Scan(&feed.ID, &feed.Title, &feed.URL, &feed.FolderID, &feed.FolderName, &feed.UnreadCount, &feed.LastCheckedAt, &feed.LastError); err != nil {
			return nil, err
		}
		result = append(result, feed)
	}
	return result, rows.Err()
}

func (s *Store) CreateFeed(ctx context.Context, rawURL string, document feeds.Document, folderID *int64) (Feed, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Feed{}, err
	}
	defer tx.Rollback(ctx)

	if folderID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id = $1)`, *folderID).Scan(&exists); err != nil {
			return Feed{}, err
		}
		if !exists {
			return Feed{}, ErrFolderUnavailable
		}
	}

	var feed Feed
	err = tx.QueryRow(ctx, `
		INSERT INTO feeds (title, url, site_url, folder_id, last_checked_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (url) DO NOTHING
		RETURNING id, title, url, folder_id`, document.Title, rawURL, document.SiteURL, folderID).
		Scan(&feed.ID, &feed.Title, &feed.URL, &feed.FolderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Feed{}, ErrDuplicate
	}
	if err != nil {
		return Feed{}, err
	}
	if err := upsertItems(ctx, tx, feed.ID, document.Items); err != nil {
		return Feed{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Feed{}, err
	}
	return feed, nil
}

func (s *Store) ListArticles(ctx context.Context, filter string, feedID, folderID *int64, query string, since *time.Time, limit, offset int) ([]Article, error) {
	conditions := []string{"TRUE"}
	args := make([]any, 0, 7)
	add := func(value any) string {
		args = append(args, value)
		return "$" + itoa(len(args))
	}
	switch filter {
	case "unread":
		conditions = append(conditions, "a.is_read = FALSE")
	case "saved":
		conditions = append(conditions, "a.is_saved = TRUE")
	case "today":
		if since != nil {
			conditions = append(conditions, "a.published_at >= "+add(*since))
		} else {
			conditions = append(conditions, "a.published_at >= date_trunc('day', NOW())")
		}
	}
	if feedID != nil {
		conditions = append(conditions, "a.feed_id = "+add(*feedID))
	}
	if folderID != nil {
		conditions = append(conditions, "f.folder_id = "+add(*folderID))
	}
	if query != "" {
		conditions = append(conditions, "STRPOS(LOWER(a.title || ' ' || a.summary), LOWER("+add(query)+")) > 0")
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	if offset < 0 || offset > 100000 {
		offset = 0
	}
	limitArg := add(limit + 1)
	offsetArg := add(offset)

	statement := `
		SELECT a.id, a.feed_id, f.title, f.url, a.title, a.url, a.summary,
		       a.published_at, a.is_read, a.is_saved
		FROM articles a JOIN feeds f ON f.id = a.feed_id
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY a.published_at DESC, a.id DESC
		LIMIT ` + limitArg + ` OFFSET ` + offsetArg
	rows, err := s.pool.Query(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Article, 0)
	for rows.Next() {
		var article Article
		if err := rows.Scan(&article.ID, &article.FeedID, &article.FeedTitle, &article.FeedURL, &article.Title, &article.URL, &article.Summary, &article.PublishedAt, &article.IsRead, &article.IsSaved); err != nil {
			return nil, err
		}
		result = append(result, article)
	}
	return result, rows.Err()
}

func (s *Store) UpdateArticle(ctx context.Context, id int64, isRead, isSaved *bool) (Article, error) {
	_, err := s.pool.Exec(ctx, `UPDATE articles SET is_read = COALESCE($2, is_read), is_saved = COALESCE($3, is_saved) WHERE id = $1`, id, isRead, isSaved)
	if err != nil {
		return Article{}, err
	}
	return s.GetArticle(ctx, id)
}

func (s *Store) GetArticle(ctx context.Context, id int64) (Article, error) {
	var article Article
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.feed_id, f.title, f.url, a.title, a.url, a.summary,
		       a.published_at, a.is_read, a.is_saved
		FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE a.id = $1`, id).
		Scan(&article.ID, &article.FeedID, &article.FeedTitle, &article.FeedURL, &article.Title, &article.URL, &article.Summary, &article.PublishedAt, &article.IsRead, &article.IsSaved)
	if errors.Is(err, pgx.ErrNoRows) {
		return Article{}, ErrNotFound
	}
	return article, err
}

func (s *Store) DueFeeds(ctx context.Context) ([]FeedTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, url FROM feeds
		WHERE last_checked_at IS NULL OR last_checked_at < NOW() - INTERVAL '15 minutes'
		ORDER BY last_checked_at NULLS FIRST, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]FeedTarget, 0)
	for rows.Next() {
		var target FeedTarget
		if err := rows.Scan(&target.ID, &target.URL); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *Store) AllFeeds(ctx context.Context) ([]FeedTarget, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, url FROM feeds ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]FeedTarget, 0)
	for rows.Next() {
		var target FeedTarget
		if err := rows.Scan(&target.ID, &target.URL); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *Store) ApplyRefresh(ctx context.Context, feedID int64, document feeds.Document) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := upsertItems(ctx, tx, feedID, document.Items); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE feeds SET title = $2, site_url = $3, last_checked_at = NOW(), last_error = NULL WHERE id = $1`, feedID, document.Title, document.SiteURL)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetRefreshError(ctx context.Context, feedID int64, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE feeds SET last_checked_at = NOW(), last_error = $2 WHERE id = $1`, feedID, message)
	return err
}

func upsertItems(ctx context.Context, tx pgx.Tx, feedID int64, items []feeds.Item) error {
	for _, item := range items {
		_, err := tx.Exec(ctx, `
			INSERT INTO articles (feed_id, guid, title, url, summary, published_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (feed_id, guid) DO UPDATE SET
				title = EXCLUDED.title,
				url = EXCLUDED.url,
				summary = EXCLUDED.summary,
				published_at = CASE WHEN $7 THEN EXCLUDED.published_at ELSE articles.published_at END`,
			feedID, item.GUID, item.Title, item.URL, item.Summary, item.PublishedAt, item.HasPublishedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
