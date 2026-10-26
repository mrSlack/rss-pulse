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
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	ErrQuarantineUnavailable  = errors.New("quarantined article cannot be moderated")
	ErrNotFound               = errors.New("record not found")
	ErrDuplicate              = errors.New("an object with that name or URL already exists")
	ErrInvalidFeedURL         = errors.New("feed URL must be an absolute HTTP or HTTPS URL")
	ErrBootstrapRequired      = errors.New("INITIAL_ADMIN_EMAIL and INITIAL_ADMIN_PASSWORD are required because the database has no users")
	ErrLeaseLost              = errors.New("refresh lease is no longer owned by this worker")
	ErrFolderUnavailable      = errors.New("selected folder is unavailable")
	ErrInvalidFolderOperation = errors.New("invalid folder deletion operation")
	ErrFeedLimitReached       = errors.New("the user has reached the feed subscription limit")
	ErrLastAdministrator      = errors.New("cannot demote the last Administrator")
	ErrInvalidStopWord        = errors.New("stop word must contain between 1 and 80 letters or digits")
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
