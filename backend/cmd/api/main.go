package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

func main() {
	connectionString := databaseURL()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connectDatabase(ctx, connectionString)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer pool.Close()

	if err := store.Migrate(ctx, pool); err != nil {
		log.Fatalf("database migration failed: %v", err)
	}
	administratorID, err := store.EnsureInitialAdministrator(ctx, pool, os.Getenv("INITIAL_ADMIN_EMAIL"), os.Getenv("INITIAL_ADMIN_PASSWORD"))
	if err != nil {
		log.Fatalf("initial administrator setup failed: %v; set INITIAL_ADMIN_EMAIL and INITIAL_ADMIN_PASSWORD in .env for an empty database", err)
	}
	if err := store.MigrateSharedSources(ctx, pool, administratorID); err != nil {
		log.Fatalf("shared-source migration failed: %v", err)
	}
	repository := store.New(pool)
	refreshControl := syncer.NewControl()
	refreshConcurrency := syncer.ConcurrencyFromEnv()
	log.Printf("rss refresh concurrency: %d", refreshConcurrency)
	go syncer.New(repository, refreshControl, refreshConcurrency).Run(ctx)

	cookieSecure := false
	if raw := os.Getenv("SESSION_COOKIE_SECURE"); raw != "" {
		cookieSecure, err = strconv.ParseBool(raw)
		if err != nil {
			log.Fatalf("SESSION_COOKIE_SECURE must be true or false: %v", err)
		}
	}
	handler := httpapi.New(repository, refreshControl, cookieSecure)
	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("Pulse API listening on %s", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server failed: %v", err)
		}
	}
}

func databaseURL() string {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
	defaultValue := func(key, fallback string) string {
		if value := os.Getenv(key); value != "" {
			return value
		}
		return fallback
	}
	connection := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(defaultValue("POSTGRES_USER", "pulse"), defaultValue("POSTGRES_PASSWORD", "pulse-local-only")),
		Host:   net.JoinHostPort(defaultValue("DB_HOST", "127.0.0.1"), defaultValue("DB_PORT", "5432")),
		Path:   "/" + defaultValue("POSTGRES_DB", "pulse"),
	}
	query := connection.Query()
	query.Set("sslmode", "disable")
	connection.RawQuery = query.Encode()
	return connection.String()
}

func connectDatabase(ctx context.Context, connectionString string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	deadline := time.NewTimer(60 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		pingContext, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := pool.Ping(pingContext)
		cancel()
		if err == nil {
			return pool, nil
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-deadline.C:
			pool.Close()
			return nil, errors.New("database was unavailable for 60 seconds")
		case <-ticker.C:
		}
	}
}
