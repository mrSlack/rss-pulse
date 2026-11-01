// Package metrics defines the Prometheus metrics of the Pulse API and the handler that
// exposes them. The handler is meant for a separate listener (METRICS_ADDR) that is not
// published through the public Nginx site.
//
// Every label has a small, fixed set of values (route patterns, methods, status
// classes, outcomes and kinds); user IDs, e-mail addresses, URLs and feed IDs are
// never labels. All methods of a nil *Metrics do nothing, so instrumented code runs
// unchanged when metrics are disabled.
package metrics

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Feed refresh outcomes (label outcome of pulse_feed_refresh_attempts_total).
const (
	RefreshOK          = "ok"
	RefreshNotModified = "not_modified"
	RefreshError       = "error"
)

// Sign-in outcomes (label outcome of pulse_signin_attempts_total).
const (
	SignInSuccess     = "success"
	SignInFailure     = "failure"
	SignInRateLimited = "rate_limited"
	SignInError       = "error"
)

// Maintenance cleanup kinds (label kind of pulse_maintenance_deleted_rows_total).
const (
	MaintenanceArticles                   = "articles"
	MaintenanceQuarantine                 = "quarantine"
	MaintenanceUnsubscribedSources        = "unsubscribed_sources"
	MaintenanceUnsubscribedSourceArticles = "unsubscribed_source_articles"
	MaintenanceSessions                   = "sessions"
	MaintenanceLoginAttempts              = "login_attempts"
)

// UnmatchedRoute is the route label of requests that matched no API route.
const UnmatchedRoute = "unmatched"

// DefaultOperationalStatsTTL is how long the database-derived gauges are reused
// between scrapes before they are queried again.
const DefaultOperationalStatsTTL = 15 * time.Second

const operationalStatsTimeout = 3 * time.Second

// OverdueTolerance is how late a due source may be before it counts as overdue
// (pulse_feed_sources_overdue). The scheduler checks for due sources every minute and
// a fetch may take 15 seconds, so a healthy instance stays well below it.
const OverdueTolerance = 5 * time.Minute

// Metrics holds the registry and every Pulse collector.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec

	refreshAttempts     *prometheus.CounterVec
	fetchDuration       prometheus.Histogram
	articlesInserted    prometheus.Counter
	articlesQuarantined prometheus.Counter

	signIns *prometheus.CounterVec

	maintenanceRuns        *prometheus.CounterVec
	maintenanceDuration    prometheus.Gauge
	maintenanceLastSuccess prometheus.Gauge
	maintenanceDeleted     *prometheus.CounterVec

	searchUpgradeDone       prometheus.Gauge
	searchUpgradeBackfilled prometheus.Counter
}

// New creates the metrics with a new registry that also contains the Go runtime and
// process collectors.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pulse_http_requests_total",
			Help: "API requests by chi route pattern, method and status class.",
		}, []string{"route", "method", "status_class"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pulse_http_request_duration_seconds",
			Help:    "API request duration by chi route pattern, method and status class.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "status_class"}),
		refreshAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pulse_feed_refresh_attempts_total",
			Help: "Feed source refresh attempts that claimed a source, by outcome (ok, not_modified, error).",
		}, []string{"outcome"}),
		fetchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "pulse_feed_fetch_duration_seconds",
			Help:    "Duration of feed downloads, successful or not.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 4, 8, 15},
		}),
		articlesInserted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pulse_feed_articles_inserted_total",
			Help: "Articles inserted by feed refreshes.",
		}),
		articlesQuarantined: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pulse_feed_articles_quarantined_total",
			Help: "Feed items newly held in quarantine by the stop-word filter.",
		}),
		signIns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pulse_signin_attempts_total",
			Help: "Sign-in attempts by outcome (success, failure, rate_limited, error).",
		}, []string{"outcome"}),
		maintenanceRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pulse_maintenance_runs_total",
			Help: "Retention maintenance runs by outcome (success, failure).",
		}, []string{"outcome"}),
		maintenanceDuration: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_maintenance_last_run_duration_seconds",
			Help: "Duration of the most recent retention maintenance run.",
		}),
		maintenanceLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_maintenance_last_success_timestamp_seconds",
			Help: "Unix time of the end of the most recent maintenance run without failures (0 = none since start).",
		}),
		maintenanceDeleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pulse_maintenance_deleted_rows_total",
			Help: "Rows deleted by retention maintenance, by kind.",
		}, []string{"kind"}),
		searchUpgradeDone: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pulse_search_upgrade_done",
			Help: "1 once the background article search upgrade has completed, else 0.",
		}),
		searchUpgradeBackfilled: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pulse_search_upgrade_backfilled_articles_total",
			Help: "Articles whose search key was filled by the background search upgrade since start.",
		}),
	}
	for _, outcome := range []string{RefreshOK, RefreshNotModified, RefreshError} {
		m.refreshAttempts.WithLabelValues(outcome)
	}
	for _, outcome := range []string{SignInSuccess, SignInFailure, SignInRateLimited, SignInError} {
		m.signIns.WithLabelValues(outcome)
	}
	for _, outcome := range []string{"success", "failure"} {
		m.maintenanceRuns.WithLabelValues(outcome)
	}
	for _, kind := range []string{MaintenanceArticles, MaintenanceQuarantine, MaintenanceUnsubscribedSources, MaintenanceUnsubscribedSourceArticles, MaintenanceSessions, MaintenanceLoginAttempts} {
		m.maintenanceDeleted.WithLabelValues(kind)
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests, m.httpDuration,
		m.refreshAttempts, m.fetchDuration, m.articlesInserted, m.articlesQuarantined,
		m.signIns,
		m.maintenanceRuns, m.maintenanceDuration, m.maintenanceLastSuccess, m.maintenanceDeleted,
		m.searchUpgradeDone, m.searchUpgradeBackfilled,
	)
	return m
}

// Registry returns the registry with every collector, for tests and custom handlers.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

// Handler serves the text exposition at GET /metrics and 404 for every other path.
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.InstrumentMetricHandler(m.registry, promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		MaxRequestsInFlight: 4,
		Timeout:             10 * time.Second,
	})))
	return mux
}

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// ObserveHTTPRequest records one API request. route is the chi route pattern; an
// empty pattern is recorded as UnmatchedRoute and unknown methods as OTHER, so
// clients cannot create label values. Status 0 means 200 (nothing written).
func (m *Metrics) ObserveHTTPRequest(route, method string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	if route == "" {
		route = UnmatchedRoute
	}
	if !knownMethods[method] {
		method = "OTHER"
	}
	if status == 0 {
		status = http.StatusOK
	}
	class := "other"
	if status >= 100 && status < 600 {
		class = strconv.Itoa(status/100) + "xx"
	}
	m.httpRequests.WithLabelValues(route, method, class).Inc()
	m.httpDuration.WithLabelValues(route, method, class).Observe(duration.Seconds())
}

// ObserveRefresh records the outcome of one claimed feed source refresh.
func (m *Metrics) ObserveRefresh(outcome string) {
	if m == nil {
		return
	}
	m.refreshAttempts.WithLabelValues(outcome).Inc()
}

// ObserveFetchDuration records the duration of one feed download.
func (m *Metrics) ObserveFetchDuration(duration time.Duration) {
	if m == nil {
		return
	}
	m.fetchDuration.Observe(duration.Seconds())
}

// AddArticles records the articles inserted and newly quarantined by a refresh.
func (m *Metrics) AddArticles(inserted, quarantined int) {
	if m == nil {
		return
	}
	m.articlesInserted.Add(float64(inserted))
	m.articlesQuarantined.Add(float64(quarantined))
}

// ObserveSignIn records the outcome of one sign-in request.
func (m *Metrics) ObserveSignIn(outcome string) {
	if m == nil {
		return
	}
	m.signIns.WithLabelValues(outcome).Inc()
}

// ObserveMaintenance records a maintenance run: its duration, the rows deleted by
// kind and whether it failed. A run without error sets the last-success time.
func (m *Metrics) ObserveMaintenance(duration time.Duration, deleted map[string]int64, err error) {
	if m == nil {
		return
	}
	for kind, rows := range deleted {
		m.maintenanceDeleted.WithLabelValues(kind).Add(float64(rows))
	}
	m.maintenanceDuration.Set(duration.Seconds())
	if err != nil {
		m.maintenanceRuns.WithLabelValues("failure").Inc()
		return
	}
	m.maintenanceRuns.WithLabelValues("success").Inc()
	m.maintenanceLastSuccess.SetToCurrentTime()
}

// SetSearchUpgradeDone marks the background article search upgrade as complete.
func (m *Metrics) SetSearchUpgradeDone() {
	if m == nil {
		return
	}
	m.searchUpgradeDone.Set(1)
}

// AddSearchKeysBackfilled records articles filled by one search upgrade batch.
func (m *Metrics) AddSearchKeysBackfilled(rows int64) {
	if m == nil {
		return
	}
	m.searchUpgradeBackfilled.Add(float64(rows))
}

// RegisterManualRefreshQueue reports the number of queued manual refresh requests,
// read on every scrape.
func (m *Metrics) RegisterManualRefreshQueue(length func() int) {
	if m == nil {
		return
	}
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "pulse_feed_manual_refresh_queue_length",
		Help: "Manual refresh requests waiting for the refresh worker.",
	}, func() float64 { return float64(length()) }))
}

// RegisterPool reports the statistics of a pgx connection pool, read on every scrape.
func (m *Metrics) RegisterPool(stat func() *pgxpool.Stat) {
	if m == nil {
		return
	}
	m.registry.MustRegister(&poolCollector{stat: stat})
}

// OperationalStats are database-derived values about the refresh backlog and the
// moderation queues.
type OperationalStats struct {
	// SourcesDue counts subscribed sources whose refresh interval has elapsed.
	SourcesDue int64
	// SourcesOverdue counts due sources whose refresh is late by more than the
	// tolerance passed to the query (the scheduler is not keeping up).
	SourcesOverdue int64
	// OldestOverdue is how long the longest-waiting due source has been due.
	OldestOverdue time.Duration
	// ActiveLeases counts sources being refreshed right now (unexpired leases).
	ActiveLeases int64
	// PendingFeedRequests counts feed requests waiting for an administrator.
	PendingFeedRequests int64
	// PendingQuarantine counts quarantined articles waiting for a decision.
	PendingQuarantine int64
}

// RegisterOperationalStats reports the values returned by query. query runs at most
// once per ttl, during a scrape, so frequent scrapes do not load the database; a
// failed query keeps the previous values and counts an error.
func (m *Metrics) RegisterOperationalStats(query func(context.Context) (OperationalStats, error), ttl time.Duration) {
	if m == nil {
		return
	}
	m.registry.MustRegister(newOperationalCollector(query, ttl))
}

var (
	poolAcquiredDesc      = prometheus.NewDesc("pulse_db_pool_acquired_connections", "Database connections currently in use.", nil, nil)
	poolIdleDesc          = prometheus.NewDesc("pulse_db_pool_idle_connections", "Idle database connections in the pool.", nil, nil)
	poolTotalDesc         = prometheus.NewDesc("pulse_db_pool_total_connections", "Database connections in the pool (acquired, idle and being opened).", nil, nil)
	poolMaxDesc           = prometheus.NewDesc("pulse_db_pool_max_connections", "Maximum size of the database pool.", nil, nil)
	poolAcquiresDesc      = prometheus.NewDesc("pulse_db_pool_acquires_total", "Successful connection acquisitions from the pool.", nil, nil)
	poolAcquireTimeDesc   = prometheus.NewDesc("pulse_db_pool_acquire_duration_seconds_total", "Total time spent acquiring connections.", nil, nil)
	poolWaitsDesc         = prometheus.NewDesc("pulse_db_pool_empty_acquires_total", "Acquisitions that had to wait because no idle connection was available.", nil, nil)
	poolWaitTimeDesc      = prometheus.NewDesc("pulse_db_pool_empty_acquire_wait_seconds_total", "Total time acquisitions waited for a connection.", nil, nil)
	poolCanceledDesc      = prometheus.NewDesc("pulse_db_pool_canceled_acquires_total", "Acquisitions canceled by their context while waiting.", nil, nil)
	sourcesDueDesc        = prometheus.NewDesc("pulse_feed_sources_due", "Subscribed feed sources whose refresh interval has elapsed.", nil, nil)
	sourcesOverdueDesc    = prometheus.NewDesc("pulse_feed_sources_overdue", "Due feed sources whose refresh is more than 5 minutes late.", nil, nil)
	oldestOverdueDesc     = prometheus.NewDesc("pulse_feed_oldest_overdue_seconds", "How long the longest-waiting due feed source has been due (0 = none).", nil, nil)
	activeLeasesDesc      = prometheus.NewDesc("pulse_feed_active_leases", "Feed sources being refreshed right now.", nil, nil)
	pendingRequestsDesc   = prometheus.NewDesc("pulse_feed_requests_pending", "Feed requests waiting for an administrator.", nil, nil)
	pendingQuarantineDesc = prometheus.NewDesc("pulse_quarantine_pending", "Quarantined articles waiting for an administrator decision.", nil, nil)
)

type poolCollector struct {
	stat func() *pgxpool.Stat
}

func (c *poolCollector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range []*prometheus.Desc{poolAcquiredDesc, poolIdleDesc, poolTotalDesc, poolMaxDesc, poolAcquiresDesc, poolAcquireTimeDesc, poolWaitsDesc, poolWaitTimeDesc, poolCanceledDesc} {
		descriptions <- description
	}
}

func (c *poolCollector) Collect(metrics chan<- prometheus.Metric) {
	stat := c.stat()
	metrics <- prometheus.MustNewConstMetric(poolAcquiredDesc, prometheus.GaugeValue, float64(stat.AcquiredConns()))
	metrics <- prometheus.MustNewConstMetric(poolIdleDesc, prometheus.GaugeValue, float64(stat.IdleConns()))
	metrics <- prometheus.MustNewConstMetric(poolTotalDesc, prometheus.GaugeValue, float64(stat.TotalConns()))
	metrics <- prometheus.MustNewConstMetric(poolMaxDesc, prometheus.GaugeValue, float64(stat.MaxConns()))
	metrics <- prometheus.MustNewConstMetric(poolAcquiresDesc, prometheus.CounterValue, float64(stat.AcquireCount()))
	metrics <- prometheus.MustNewConstMetric(poolAcquireTimeDesc, prometheus.CounterValue, stat.AcquireDuration().Seconds())
	metrics <- prometheus.MustNewConstMetric(poolWaitsDesc, prometheus.CounterValue, float64(stat.EmptyAcquireCount()))
	metrics <- prometheus.MustNewConstMetric(poolWaitTimeDesc, prometheus.CounterValue, stat.EmptyAcquireWaitTime().Seconds())
	metrics <- prometheus.MustNewConstMetric(poolCanceledDesc, prometheus.CounterValue, float64(stat.CanceledAcquireCount()))
}

type operationalCollector struct {
	query  func(context.Context) (OperationalStats, error)
	ttl    time.Duration
	errors prometheus.Counter

	mu        sync.Mutex
	queriedAt time.Time
	stats     OperationalStats
	valid     bool
}

func newOperationalCollector(query func(context.Context) (OperationalStats, error), ttl time.Duration) *operationalCollector {
	return &operationalCollector{query: query, ttl: ttl, errors: prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pulse_operational_stats_errors_total",
		Help: "Failed database queries for the refresh backlog and moderation queue gauges.",
	})}
}

func (c *operationalCollector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range []*prometheus.Desc{sourcesDueDesc, sourcesOverdueDesc, oldestOverdueDesc, activeLeasesDesc, pendingRequestsDesc, pendingQuarantineDesc} {
		descriptions <- description
	}
	c.errors.Describe(descriptions)
}

func (c *operationalCollector) Collect(metrics chan<- prometheus.Metric) {
	stats, ok := c.current()
	metrics <- c.errors
	if !ok {
		return
	}
	metrics <- prometheus.MustNewConstMetric(sourcesDueDesc, prometheus.GaugeValue, float64(stats.SourcesDue))
	metrics <- prometheus.MustNewConstMetric(sourcesOverdueDesc, prometheus.GaugeValue, float64(stats.SourcesOverdue))
	metrics <- prometheus.MustNewConstMetric(oldestOverdueDesc, prometheus.GaugeValue, max(stats.OldestOverdue, 0).Seconds())
	metrics <- prometheus.MustNewConstMetric(activeLeasesDesc, prometheus.GaugeValue, float64(stats.ActiveLeases))
	metrics <- prometheus.MustNewConstMetric(pendingRequestsDesc, prometheus.GaugeValue, float64(stats.PendingFeedRequests))
	metrics <- prometheus.MustNewConstMetric(pendingQuarantineDesc, prometheus.GaugeValue, float64(stats.PendingQuarantine))
}

// current returns cached stats, querying them when the cache has expired. The lock
// is held during the query so concurrent scrapes share one query.
func (c *operationalCollector) current() (OperationalStats, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && c.ttl > 0 && time.Since(c.queriedAt) < c.ttl {
		return c.stats, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), operationalStatsTimeout)
	defer cancel()
	stats, err := c.query(ctx)
	if err != nil {
		c.errors.Inc()
		return c.stats, c.valid
	}
	c.stats, c.valid, c.queriedAt = stats, true, time.Now()
	return stats, true
}
