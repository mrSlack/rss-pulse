package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pulse-reader/app/internal/metrics"
)

// apiPort is the port of the public API listener; the metrics listener must use another.
const apiPort = 8080

// metricsAddrFromEnv reads METRICS_ADDR, the host:port of the separate Prometheus
// listener. Empty disables metrics. Invalid values return an error so startup fails fast.
func metricsAddrFromEnv(getenv func(string) string) (string, error) {
	raw := strings.TrimSpace(getenv("METRICS_ADDR"))
	if raw == "" {
		return "", nil
	}
	_, portText, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("METRICS_ADDR must be host:port or :port, for example :9090")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("METRICS_ADDR must use a port from 1 to 65535")
	}
	if port == apiPort {
		return "", fmt.Errorf("METRICS_ADDR must not use the API port %d", apiPort)
	}
	return raw, nil
}

// newMetricsServer creates the listener that serves GET /metrics and nothing else. It
// is separate from the API server so the public Nginx site, which proxies only the API
// port, never exposes metrics.
func newMetricsServer(addr string, m *metrics.Metrics, logger *slog.Logger) *http.Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &http.Server{
		Addr:              addr,
		Handler:           m.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}
