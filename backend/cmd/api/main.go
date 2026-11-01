package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/pulse-reader/app/internal/maintenance"
	"github.com/pulse-reader/app/internal/metrics"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

func main() {
	logger, err := newLogger(os.Getenv, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid logging configuration: %v\n", err)
		os.Exit(1)
	}
	// Workers and libraries that use slog.Default or the log package write
	// through the same handler.
	slog.SetDefault(logger)
	fatal := func(message string, err error, attributes ...any) {
		logger.Error(message, append([]any{slog.String("error", err.Error())}, attributes...)...)
		os.Exit(1)
	}

	connectionString := databaseURL()
	trustedProxies, err := httpapi.ParseTrustedProxyCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		fatal("TRUSTED_PROXY_CIDRS must be a comma-separated list of CIDRs or \"none\"", err)
	}
	retention, err := maintenance.ConfigFromEnv(os.Getenv)
	if err != nil {
		fatal("invalid retention configuration", err)
	}
	poolMaxConns, err := poolMaxConnsFromEnv(os.Getenv)
	if err != nil {
		fatal("invalid database pool configuration", err)
	}
	metricsAddr, err := metricsAddrFromEnv(os.Getenv)
	if err != nil {
		fatal("invalid metrics configuration", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connectDatabase(ctx, connectionString, poolMaxConns)
	if err != nil {
		fatal("database connection failed", err)
	}
	defer pool.Close()

	if err := store.Migrate(ctx, pool); err != nil {
		fatal("database migration failed", err)
	}
	administratorID, err := store.EnsureInitialAdministrator(ctx, pool, os.Getenv("INITIAL_ADMIN_EMAIL"), os.Getenv("INITIAL_ADMIN_PASSWORD"))
	if err != nil {
		fatal("initial administrator setup failed", err, slog.String("hint", "set INITIAL_ADMIN_EMAIL and INITIAL_ADMIN_PASSWORD in .env for an empty database"))
	}
	if err := store.MigrateSharedSources(ctx, pool, administratorID); err != nil {
		fatal("shared-source migration failed", err)
	}
	repository := store.New(pool)
	repository.SetArticleRetentionDays(retention.ArticleRetentionDays)
	refreshControl := syncer.NewControl()
	// appMetrics stays nil (recording nothing) unless METRICS_ADDR enables the listener.
	var appMetrics *metrics.Metrics
	if metricsAddr != "" {
		appMetrics = metrics.New()
		appMetrics.RegisterPool(pool.Stat)
		appMetrics.RegisterManualRefreshQueue(refreshControl.QueueLength)
		appMetrics.RegisterOperationalStats(func(ctx context.Context) (metrics.OperationalStats, error) {
			stats, err := repository.OperationalStats(ctx, metrics.OverdueTolerance)
			return metrics.OperationalStats(stats), err
		}, metrics.DefaultOperationalStatsTTL)
	}
	logger.Info("retention configured (0 = disabled)", slog.Int("article_days", retention.ArticleRetentionDays), slog.Int("quarantine_days", retention.QuarantineRetentionDays),
		slog.Int("unsubscribed_source_days", retention.UnsubscribedSourceRetentionDays))
	maintenanceDone := maintenance.Job{Repository: repository, Config: retention, InitialDelay: maintenance.DefaultInitialDelay, Interval: maintenance.DefaultInterval, Metrics: appMetrics}.Start(ctx)
	refreshConcurrency := syncer.ConcurrencyFromEnv()
	logger.Info("rss refresh configured", slog.Int("concurrency", refreshConcurrency))
	syncerDone := syncer.New(repository, refreshControl, refreshConcurrency, syncer.WithLogger(logger), syncer.WithMetrics(appMetrics)).Start(ctx)

	cookieSecure := false
	if raw := os.Getenv("SESSION_COOKIE_SECURE"); raw != "" {
		cookieSecure, err = strconv.ParseBool(raw)
		if err != nil {
			fatal("SESSION_COOKIE_SECURE must be true or false", err)
		}
	}
	handler := httpapi.New(repository, refreshControl, cookieSecure, httpapi.WithTrustedProxies(trustedProxies), httpapi.WithLogger(logger), httpapi.WithMetrics(appMetrics))
	server := &http.Server{
		Addr:              ":" + strconv.Itoa(apiPort),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("Pulse API listening", slog.String("address", server.Addr))
		serverErrors <- server.ListenAndServe()
	}()
	var metricsServer *http.Server
	if appMetrics != nil {
		metricsServer = newMetricsServer(metricsAddr, appMetrics, logger)
		go func() {
			logger.Info("Prometheus metrics listening", slog.String("address", metricsServer.Addr), slog.String("path", "/metrics"))
			serverErrors <- metricsServer.ListenAndServe()
		}()
	}
	// Long schema steps (backfill, concurrent index builds) run while the API serves, so
	// they do not delay the start; search is correct, only slower, until they finish.
	searchUpgrade := store.ArticleSearchUpgrade{Logger: logger, Pause: 10 * time.Millisecond, Progress: appMetrics.AddSearchKeysBackfilled}
	searchUpgradeDone := runBackgroundUpgrade(ctx, logger, "article search", backgroundUpgradeRetryDelay, func(ctx context.Context) error {
		err := searchUpgrade.Run(ctx, pool)
		if err == nil {
			appMetrics.SetSearchUpgradeDone()
		}
		return err
	})

	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Warn("server shutdown", slog.String("error", err.Error()))
		}
		if metricsServer != nil {
			if err := metricsServer.Shutdown(shutdownContext); err != nil {
				logger.Warn("metrics server shutdown", slog.String("error", err.Error()))
			}
		}
		waitForWorker(shutdownContext, "feed refresh", syncerDone)
		waitForWorker(shutdownContext, "maintenance", maintenanceDone)
		waitForWorker(shutdownContext, "article search upgrade", searchUpgradeDone)
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			fatal("http server failed", err)
		}
	}
}

// waitForWorker waits until a background worker has stopped or the shutdown
// deadline has passed.
func waitForWorker(ctx context.Context, name string, done <-chan struct{}) {
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("shutdown: worker did not stop before the deadline", slog.String("worker", name))
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

func connectDatabase(ctx context.Context, connectionString string, maxConns int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		return nil, err
	}
	config.MaxConns = maxConns
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
