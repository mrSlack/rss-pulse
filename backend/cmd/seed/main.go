// Command seed fills a dedicated PostgreSQL database with a large synthetic Pulse
// dataset for performance measurements. It refuses to touch a database that
// contains any account outside the reserved seed.invalid domain.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	config := defaultConfig()
	flags := flag.NewFlagSet("seed", flag.ExitOnError)
	flags.StringVar(&config.DSN, "dsn", "", "PostgreSQL connection string of the database to fill (required)")
	flags.IntVar(&config.Users, "users", config.Users, "number of users, including the seed administrator")
	flags.IntVar(&config.Sources, "sources", config.Sources, "number of shared feed sources")
	flags.IntVar(&config.ArticlesPerSource, "articles-per-source", config.ArticlesPerSource, "articles stored for each source")
	flags.IntVar(&config.SubscriptionsPerUser, "subscriptions-per-user", config.SubscriptionsPerUser, "mean subscriptions per user (±25%, at most 100)")
	flags.IntVar(&config.FoldersPerUser, "folders-per-user", config.FoldersPerUser, "folders per user")
	flags.Float64Var(&config.ReadRatio, "read-ratio", config.ReadRatio, "share of visible articles each user has read")
	flags.Float64Var(&config.SavedRatio, "saved-ratio", config.SavedRatio, "share of visible articles each user has saved")
	flags.Float64Var(&config.SearchTermRatio, "search-term-ratio", config.SearchTermRatio, "share of articles containing the word \"market\"")
	flags.IntVar(&config.HistoryDays, "history-days", config.HistoryDays, "publication dates are spread evenly over this many past days")
	flags.IntVar(&config.QuarantineRecords, "quarantine-records", config.QuarantineRecords, "blocked_feed_articles rows")
	flags.IntVar(&config.ExpiredSessionsPerUser, "expired-sessions-per-user", config.ExpiredSessionsPerUser, "expired sessions per user in addition to one active session")
	flags.Uint64Var(&config.RandomSeed, "random-seed", config.RandomSeed, "seed of the deterministic random generator")
	flags.StringVar(&config.Password, "password", config.Password, "password of every seed account")
	flags.BoolVar(&config.ConfirmDestroyData, "confirm-destroy-data", false, "required: allow replacing all data in the target database")
	_ = flags.Parse(os.Args[1:])

	ctx := context.Background()
	if err := config.validate(); err != nil {
		log.Fatal(err)
	}
	if config.DSN == "" {
		log.Fatal("-dsn is required")
	}
	pool, err := pgxpool.New(ctx, config.DSN)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	started := time.Now()
	summary, err := Seed(ctx, pool, config)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("seeded in %s: %s\n", time.Since(started).Round(time.Second), summary)
}

type Config struct {
	DSN                    string
	Users                  int
	Sources                int
	ArticlesPerSource      int
	SubscriptionsPerUser   int
	FoldersPerUser         int
	ReadRatio              float64
	SavedRatio             float64
	SearchTermRatio        float64
	HistoryDays            int
	QuarantineRecords      int
	ExpiredSessionsPerUser int
	RandomSeed             uint64
	Password               string
	ConfirmDestroyData     bool
}

func defaultConfig() Config {
	return Config{
		Users:                  50,
		Sources:                500,
		ArticlesPerSource:      1000,
		SubscriptionsPerUser:   60,
		FoldersPerUser:         6,
		ReadRatio:              0.30,
		SavedRatio:             0.02,
		SearchTermRatio:        0.02,
		HistoryDays:            400,
		QuarantineRecords:      2000,
		ExpiredSessionsPerUser: 3,
		RandomSeed:             20261101,
		Password:               "seed-only-password",
	}
}

var errNotConfirmed = errors.New("refusing to seed: pass -confirm-destroy-data to replace all data in the target database")

func (c Config) validate() error {
	if !c.ConfirmDestroyData {
		return errNotConfirmed
	}
	switch {
	case c.Users < 1 || c.Sources < 1 || c.ArticlesPerSource < 1 || c.SubscriptionsPerUser < 1 || c.FoldersPerUser < 0:
		return errors.New("users, sources, articles and subscriptions must be positive")
	case c.ReadRatio < 0 || c.ReadRatio > 1 || c.SavedRatio < 0 || c.SavedRatio > 1 || c.SearchTermRatio < 0 || c.SearchTermRatio > 1:
		return errors.New("ratios must be between 0 and 1")
	case c.HistoryDays < 1 || c.QuarantineRecords < 0 || c.ExpiredSessionsPerUser < 0:
		return errors.New("history days must be positive; counts must not be negative")
	case len([]rune(c.Password)) < 12:
		return errors.New("password must contain at least 12 characters")
	}
	return nil
}
