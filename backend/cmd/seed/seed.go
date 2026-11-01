package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/store"
)

const seedDomain = "seed.invalid"

var errForeignAccounts = errors.New("refusing to seed: the target database contains accounts outside the " + seedDomain + " domain")

// Seed replaces the contents of the database behind pool with a synthetic dataset.
// It refuses unless the configuration confirms destruction and every existing
// account belongs to the seed domain. Schema migrations are applied like at API start.
func Seed(ctx context.Context, pool *pgxpool.Pool, config Config) (string, error) {
	if err := config.validate(); err != nil {
		return "", err
	}
	if err := checkTarget(ctx, pool); err != nil {
		return "", err
	}
	if err := store.Migrate(ctx, pool); err != nil {
		return "", fmt.Errorf("migrate: %w", err)
	}
	if err := truncateExisting(ctx, pool); err != nil {
		return "", err
	}
	adminID, err := store.EnsureInitialAdministrator(ctx, pool, "admin@"+seedDomain, config.Password)
	if err != nil {
		return "", fmt.Errorf("bootstrap administrator: %w", err)
	}
	if err := store.MigrateSharedSources(ctx, pool, adminID); err != nil {
		return "", fmt.Errorf("migrate shared sources: %w", err)
	}
	// Default folders created by the first migration phase exist only on a fresh
	// database; remove them so every run produces the same dataset.
	if _, err := pool.Exec(ctx, `DELETE FROM folders`); err != nil {
		return "", err
	}
	generator := &generator{config: config, random: rand.New(rand.NewPCG(config.RandomSeed, config.RandomSeed^0x9e3779b97f4a7c15)), now: time.Now().UTC()}
	return generator.run(ctx, pool, adminID)
}

func checkTarget(ctx context.Context, pool *pgxpool.Pool) error {
	var usersTable *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('users')::text`).Scan(&usersTable); err != nil {
		return err
	}
	if usersTable == nil {
		return nil
	}
	var foreign int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE LOWER(email) NOT LIKE $1`, "%@"+seedDomain).Scan(&foreign); err != nil {
		return err
	}
	if foreign > 0 {
		return errForeignAccounts
	}
	return nil
}

func truncateExisting(ctx context.Context, pool *pgxpool.Pool) error {
	tables := []string{"user_article_states", "articles", "user_feeds", "feed_requests", "blocked_feed_articles", "feed_sources", "sessions", "folders", "users"}
	existing := make([]string, 0, len(tables))
	for _, table := range tables {
		var name *string
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, table).Scan(&name); err != nil {
			return err
		}
		if name != nil {
			existing = append(existing, pgx.Identifier{table}.Sanitize())
		}
	}
	if len(existing) == 0 {
		return nil
	}
	_, err := pool.Exec(ctx, `TRUNCATE `+strings.Join(existing, ", ")+` RESTART IDENTITY CASCADE`)
	return err
}

type generator struct {
	config Config
	random *rand.Rand
	now    time.Time
}

type subscription struct {
	userID   int64
	sourceID int64
}

func (g *generator) run(ctx context.Context, pool *pgxpool.Pool, adminID int64) (string, error) {
	passwordHash, err := auth.HashPassword(g.config.Password)
	if err != nil {
		return "", err
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET first_name='Seed',last_name='Administrator' WHERE id=$1`, adminID); err != nil {
		return "", err
	}
	userIDs := []int64{adminID}
	for index := 2; index <= g.config.Users; index++ {
		userIDs = append(userIDs, adminID+int64(index-1))
	}
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"users"}, []string{"id", "email", "password_hash", "role", "first_name", "last_name", "created_at", "updated_at"},
		pgx.CopyFromSlice(len(userIDs)-1, func(index int) ([]any, error) {
			id := userIDs[index+1]
			return []any{id, fmt.Sprintf("user%03d@%s", index+2, seedDomain), passwordHash, "Reader", "Seed", fmt.Sprintf("User %d", index+2), g.now, g.now}, nil
		})); err != nil {
		return "", fmt.Errorf("copy users: %w", err)
	}

	folderIDs, err := g.seedFolders(ctx, pool, userIDs)
	if err != nil {
		return "", err
	}
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"feed_sources"}, []string{"id", "normalized_url", "title", "url", "site_url", "last_checked_at", "last_success_at", "created_at", "added_by_user_id"},
		pgx.CopyFromSlice(g.config.Sources, func(index int) ([]any, error) {
			id := int64(index + 1)
			url := fmt.Sprintf("https://feed-%04d.%s/rss", id, seedDomain)
			checked := g.now.Add(-time.Duration(g.random.IntN(30*60)) * time.Second)
			return []any{id, url, fmt.Sprintf("Seed Feed %04d %s", id, pick(g.random, englishWords)), url, fmt.Sprintf("https://feed-%04d.%s/", id, seedDomain),
				checked, checked, g.now.AddDate(0, 0, -g.config.HistoryDays-1), userIDs[g.random.IntN(len(userIDs))]}, nil
		})); err != nil {
		return "", fmt.Errorf("copy feed sources: %w", err)
	}

	subscriptions, err := g.seedSubscriptions(ctx, pool, userIDs, folderIDs)
	if err != nil {
		return "", err
	}
	articleCount, err := g.seedArticles(ctx, pool)
	if err != nil {
		return "", err
	}
	stateCount, err := g.seedStates(ctx, pool, subscriptions)
	if err != nil {
		return "", err
	}
	sessionCount, err := g.seedSessions(ctx, pool, userIDs)
	if err != nil {
		return "", err
	}
	if err := g.seedQuarantine(ctx, pool, userIDs); err != nil {
		return "", err
	}
	for _, table := range []string{"users", "feed_sources", "articles", "folders", "user_feeds", "blocked_feed_articles"} {
		if _, err := pool.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1, 'id'), COALESCE((SELECT MAX(id) FROM `+pgx.Identifier{table}.Sanitize()+`), 1))`, table); err != nil {
			return "", fmt.Errorf("reset %s sequence: %w", table, err)
		}
	}
	if _, err := pool.Exec(ctx, `ANALYZE`); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d users, %d sources, %d subscriptions, %d articles, %d article states, %d sessions, %d quarantine records",
		len(userIDs), g.config.Sources, len(subscriptions), articleCount, stateCount, sessionCount, g.config.QuarantineRecords), nil
}

func (g *generator) seedFolders(ctx context.Context, pool *pgxpool.Pool, userIDs []int64) (map[int64][]int64, error) {
	colors := []string{"amber", "blue", "green", "rose", "violet", "teal", "#3366cc"}
	rows := make([][]any, 0, len(userIDs)*g.config.FoldersPerUser)
	for _, userID := range userIDs {
		for index := 0; index < g.config.FoldersPerUser; index++ {
			name := folderNames[index%len(folderNames)]
			if index >= len(folderNames) {
				name = fmt.Sprintf("%s %d", name, index/len(folderNames)+1)
			}
			rows = append(rows, []any{userID, name, colors[index%len(colors)]})
		}
	}
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"folders"}, []string{"owner_user_id", "name", "color"}, pgx.CopyFromRows(rows)); err != nil {
		return nil, fmt.Errorf("copy folders: %w", err)
	}
	result, err := pool.Query(ctx, `SELECT owner_user_id, id FROM folders ORDER BY owner_user_id, id`)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	folders := make(map[int64][]int64, len(userIDs))
	for result.Next() {
		var owner, id int64
		if err := result.Scan(&owner, &id); err != nil {
			return nil, err
		}
		folders[owner] = append(folders[owner], id)
	}
	return folders, result.Err()
}

func (g *generator) seedSubscriptions(ctx context.Context, pool *pgxpool.Pool, userIDs []int64, folderIDs map[int64][]int64) ([]subscription, error) {
	subscriptions := make([]subscription, 0, len(userIDs)*g.config.SubscriptionsPerUser)
	rows := make([][]any, 0, cap(subscriptions))
	limit := min(store.MaxFeedsPerUser, g.config.Sources)
	for _, userID := range userIDs {
		spread := g.config.SubscriptionsPerUser / 4
		count := g.config.SubscriptionsPerUser
		if spread > 0 {
			count += g.random.IntN(2*spread+1) - spread
		}
		count = max(1, min(count, limit))
		sources := g.random.Perm(g.config.Sources)[:count]
		for _, sourceIndex := range sources {
			sourceID := int64(sourceIndex + 1)
			var folderID *int64
			if folders := folderIDs[userID]; len(folders) > 0 && g.random.Float64() < 0.8 {
				folderID = &folders[g.random.IntN(len(folders))]
			}
			subscriptions = append(subscriptions, subscription{userID: userID, sourceID: sourceID})
			created := g.now.AddDate(0, 0, -g.random.IntN(g.config.HistoryDays))
			rows = append(rows, []any{int64(len(rows) + 1), userID, sourceID, folderID, created})
		}
	}
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"user_feeds"}, []string{"id", "user_id", "feed_source_id", "folder_id", "created_at"}, pgx.CopyFromRows(rows)); err != nil {
		return nil, fmt.Errorf("copy subscriptions: %w", err)
	}
	return subscriptions, nil
}

// articleID returns the deterministic id of an article: sources hold consecutive ids.
func (g *generator) articleID(sourceID int64, index int) int64 {
	return (sourceID-1)*int64(g.config.ArticlesPerSource) + int64(index) + 1
}

func (g *generator) seedArticles(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	total := g.config.Sources * g.config.ArticlesPerSource
	history := time.Duration(g.config.HistoryDays) * 24 * time.Hour
	step := history / time.Duration(g.config.ArticlesPerSource)
	position := 0
	count, err := pool.CopyFrom(ctx, pgx.Identifier{"articles"}, []string{"id", "feed_source_id", "guid", "title", "url", "summary", "published_at", "created_at"},
		pgx.CopyFromFunc(func() ([]any, error) {
			if position >= total {
				return nil, nil
			}
			sourceID := int64(position/g.config.ArticlesPerSource + 1)
			index := position % g.config.ArticlesPerSource
			position++
			// Index 0 is the oldest article; the newest is published within the last step.
			published := g.now.Add(-history + time.Duration(index)*step + time.Duration(g.random.Int64N(int64(step))))
			created := published.Add(time.Duration(g.random.IntN(120)) * time.Minute)
			if created.After(g.now) {
				created = g.now
			}
			withTerm := g.random.Float64() < g.config.SearchTermRatio
			title := g.title(withTerm && g.random.IntN(2) == 0)
			summary := g.summary(withTerm && !strings.Contains(strings.ToLower(title), "market"))
			id := g.articleID(sourceID, index)
			return []any{id, sourceID, fmt.Sprintf("seed-%d-%d", sourceID, index), title,
				fmt.Sprintf("https://feed-%04d.%s/articles/%d", sourceID, seedDomain, id), summary, published, created}, nil
		}))
	if err != nil {
		return 0, fmt.Errorf("copy articles: %w", err)
	}
	return count, nil
}

func (g *generator) seedStates(ctx context.Context, pool *pgxpool.Pool, subscriptions []subscription) (int64, error) {
	subscriptionIndex, articleIndex := 0, 0
	count, err := pool.CopyFrom(ctx, pgx.Identifier{"user_article_states"}, []string{"user_id", "article_id", "is_read", "is_saved", "updated_at"},
		pgx.CopyFromFunc(func() ([]any, error) {
			for subscriptionIndex < len(subscriptions) {
				current := subscriptions[subscriptionIndex]
				index := articleIndex
				articleIndex++
				if articleIndex >= g.config.ArticlesPerSource {
					articleIndex = 0
					subscriptionIndex++
				}
				isRead := g.random.Float64() < g.config.ReadRatio
				isSaved := g.random.Float64() < g.config.SavedRatio
				if isRead || isSaved {
					return []any{current.userID, g.articleID(current.sourceID, index), isRead, isSaved, g.now}, nil
				}
			}
			return nil, nil
		}))
	if err != nil {
		return 0, fmt.Errorf("copy article states: %w", err)
	}
	return count, nil
}

// SessionToken is the clear-text cookie value of the active seed session of userID.
func SessionToken(userID int64) string {
	return fmt.Sprintf("seed-session-%d", userID)
}

func (g *generator) seedSessions(ctx context.Context, pool *pgxpool.Pool, userIDs []int64) (int64, error) {
	rows := make([][]any, 0, len(userIDs)*(1+g.config.ExpiredSessionsPerUser))
	for _, userID := range userIDs {
		rows = append(rows, []any{auth.HashSessionToken(SessionToken(userID)), userID, g.now.AddDate(0, 0, 14), g.now})
		for index := 0; index < g.config.ExpiredSessionsPerUser; index++ {
			expired := g.now.Add(-time.Duration(1+g.random.IntN(24*30)) * time.Hour)
			rows = append(rows, []any{auth.HashSessionToken(fmt.Sprintf("seed-expired-%d-%d", userID, index)), userID, expired, expired.AddDate(0, 0, -14)})
		}
	}
	count, err := pool.CopyFrom(ctx, pgx.Identifier{"sessions"}, []string{"token_hash", "user_id", "expires_at", "created_at"}, pgx.CopyFromRows(rows))
	if err != nil {
		return 0, fmt.Errorf("copy sessions: %w", err)
	}
	return count, nil
}

func (g *generator) seedQuarantine(ctx context.Context, pool *pgxpool.Pool, userIDs []int64) error {
	statuses := []string{"pending", "rejected", "rejected", "allowed"}
	index := 0
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"blocked_feed_articles"},
		[]string{"feed_source_id", "article_guid", "article_title", "article_url", "feed_title", "feed_url", "added_by_user_id", "matched_term",
			"first_blocked_at", "last_blocked_at", "summary", "published_at", "status", "decided_at", "decided_by_user_id", "moderation_eligible"},
		pgx.CopyFromFunc(func() ([]any, error) {
			if index >= g.config.QuarantineRecords {
				return nil, nil
			}
			index++
			sourceID := int64(g.random.IntN(g.config.Sources) + 1)
			url := fmt.Sprintf("https://feed-%04d.%s/rss", sourceID, seedDomain)
			first := g.now.Add(-time.Duration(g.random.Int64N(int64(time.Duration(g.config.HistoryDays) * 24 * time.Hour))))
			last := first.Add(time.Duration(g.random.Int64N(int64(g.now.Sub(first)) + 1)))
			status := statuses[g.random.IntN(len(statuses))]
			var decidedAt *time.Time
			var decidedBy *int64
			if status != "pending" {
				decided := first.Add(time.Duration(g.random.Int64N(int64(g.now.Sub(first)) + 1)))
				decidedAt, decidedBy = &decided, &userIDs[0]
			}
			return []any{sourceID, fmt.Sprintf("seed-blocked-%d", index), "Blocked " + g.title(false), url + "/blocked",
				fmt.Sprintf("Seed Feed %04d", sourceID), url, userIDs[g.random.IntN(len(userIDs))], "xxx",
				first, last, g.summary(false), first, status, decidedAt, decidedBy, g.random.IntN(10) != 0}, nil
		}))
	if err != nil {
		return fmt.Errorf("copy quarantine: %w", err)
	}
	return nil
}

func (g *generator) title(withTerm bool) string {
	words := make([]string, 0, 10)
	for range 5 + g.random.IntN(6) {
		words = append(words, g.word())
	}
	if withTerm {
		words[g.random.IntN(len(words))] = "market"
	}
	title := []rune(strings.Join(words, " "))
	title[0] = unicode.ToUpper(title[0])
	return string(title)
}

// summary returns 1–2 KB of HTML-like text mixing English, German, and Russian words.
func (g *generator) summary(withTerm bool) string {
	target := 1024 + g.random.IntN(1024)
	var builder strings.Builder
	builder.Grow(target + 256)
	termWritten := !withTerm
	for builder.Len() < target {
		builder.WriteString("<p>")
		for sentence := 0; sentence < 2+g.random.IntN(3) && builder.Len() < target; sentence++ {
			words := 8 + g.random.IntN(10)
			for index := 0; index < words; index++ {
				if index > 0 {
					builder.WriteByte(' ')
				}
				word := g.word()
				if !termWritten && g.random.IntN(4) == 0 {
					word, termWritten = "market", true
				}
				switch g.random.IntN(25) {
				case 0:
					fmt.Fprintf(&builder, `<a href="https://example.%s/%s">%s</a>`, seedDomain, word, word)
				case 1:
					builder.WriteString("<strong>" + word + "</strong>")
				default:
					builder.WriteString(word)
				}
			}
			builder.WriteString(". ")
		}
		builder.WriteString("</p>")
	}
	if !termWritten {
		builder.WriteString("<p>market</p>")
	}
	return builder.String()
}

func (g *generator) word() string {
	switch roll := g.random.IntN(10); {
	case roll < 5:
		return pick(g.random, englishWords)
	case roll < 8:
		return pick(g.random, germanWords)
	default:
		return pick(g.random, russianWords)
	}
}

func pick(random *rand.Rand, words []string) string {
	return words[random.IntN(len(words))]
}

var folderNames = []string{"Technology", "Science", "Design", "Nachrichten", "Новости", "Economy", "Kultur", "Спорт"}

// The vocabularies deliberately omit "market" so the -search-term-ratio controls its frequency.
var englishWords = strings.Fields(`the a of and to in for on with as by at from about after before between new report update
analysis release city government policy research data team project company energy climate health school
software network security developer open source community budget election weather travel music film
science space study university model system service customer price growth trade industry bank
season player match result review guide interview opinion feature launch plan public local global`)

var germanWords = strings.Fields(`der die das und nicht mit für auf ist im dem den von zu eine einen bericht neue stadt regierung
forschung daten unternehmen energie klima gesundheit schule entwicklung sicherheit gemeinschaft haushalt
wahl wetter reise musik wissenschaft studie hochschule dienst kunde preis wachstum handel industrie
spiel ergebnis überblick meinung veröffentlichung öffentlich straße brücke zukunft änderung größe`)

var russianWords = strings.Fields(`и в не на с по для из от до как это новый отчёт город правительство политика исследование данные
команда проект компания энергия климат здоровье школа разработка безопасность сообщество бюджет выборы
погода путешествие музыка наука изучение университет система сервис клиент цена рост торговля
промышленность банк сезон игрок матч результат обзор интервью мнение запуск план общественный`)
