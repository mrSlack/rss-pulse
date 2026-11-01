package main

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/pulse-reader/app/internal/metrics"
)

func TestMetricsAddrFromEnv(t *testing.T) {
	valid := map[string]string{"": "", "  ": "", ":9090": ":9090", " 0.0.0.0:9100 ": "0.0.0.0:9100", "127.0.0.1:9090": "127.0.0.1:9090", "[::1]:9090": "[::1]:9090"}
	for raw, want := range valid {
		got, err := metricsAddrFromEnv(func(key string) string {
			if key != "METRICS_ADDR" {
				t.Fatalf("read unexpected variable %q", key)
			}
			return raw
		})
		if err != nil || got != want {
			t.Errorf("METRICS_ADDR=%q: got (%q, %v), want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"9090", ":0", ":65536", ":http", "localhost", ":8080", "0.0.0.0:8080", "host:90:90"} {
		if got, err := metricsAddrFromEnv(func(string) string { return raw }); err == nil {
			t.Errorf("METRICS_ADDR=%q: got %q, want an error", raw, got)
		}
	}
}

func TestMetricsServerServesOnlyMetrics(t *testing.T) {
	m := metrics.New()
	m.ObserveSignIn(metrics.SignInSuccess)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := newMetricsServer(listener.Addr().String(), m, nil)
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	response, err := http.Get("http://" + listener.Addr().String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `pulse_signin_attempts_total{outcome="success"} 1`) {
		t.Fatalf("GET /metrics = %d:\n%s", response.StatusCode, body)
	}
	response, err = http.Get("http://" + listener.Addr().String() + "/api/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /api/health/ready on the metrics listener = %d, want 404", response.StatusCode)
	}
}
