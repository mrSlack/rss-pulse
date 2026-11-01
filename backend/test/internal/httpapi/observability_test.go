package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

var generatedRequestID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func bufferLogger(buffer *bytes.Buffer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buffer, &slog.HandlerOptions{Level: level}))
}

func logRecords(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(buffer.Bytes()))
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("log line %q is not JSON: %v", scanner.Text(), err)
		}
		records = append(records, record)
	}
	return records
}

func accessRecord(t *testing.T, buffer *bytes.Buffer, path string) map[string]any {
	t.Helper()
	var found map[string]any
	for _, record := range logRecords(t, buffer) {
		if record["msg"] == "http request" && record["path"] == path {
			if found != nil {
				t.Fatalf("more than one access log record for %s", path)
			}
			found = record
		}
	}
	if found == nil {
		t.Fatalf("no access log record for %s in %s", path, buffer.String())
	}
	return found
}

func TestAccessLogRecordsAuthenticatedRequestWithoutQueryString(t *testing.T) {
	pool, adminID, _ := isolatedAPIDatabase(t)
	var logs bytes.Buffer
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false, httpapi.WithLogger(bufferLogger(&logs, slog.LevelInfo)))
	cookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	logs.Reset()

	response := authenticatedRequest(t, handler, http.MethodGet, "/api/articles?q=private-search-term&limit=5", "", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/articles = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(logs.String(), "private-search-term") {
		t.Fatalf("access log contains the query string: %s", logs.String())
	}
	record := accessRecord(t, &logs, "/api/articles")
	requestID := response.Header().Get("X-Request-ID")
	if !generatedRequestID.MatchString(requestID) {
		t.Fatalf("X-Request-ID = %q, want generated 32-character hex ID", requestID)
	}
	want := map[string]any{
		"level": "INFO", "method": "GET", "status": float64(http.StatusOK),
		"bytes": float64(response.Body.Len()), "request_id": requestID, "user_id": float64(adminID),
	}
	for key, value := range want {
		if record[key] != value {
			t.Fatalf("access log %s = %#v, want %#v (record %v)", key, record[key], value, record)
		}
	}
	if duration, ok := record["duration_ms"].(float64); !ok || duration < 0 {
		t.Fatalf("access log duration_ms = %#v", record["duration_ms"])
	}

	logs.Reset()
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/folders", nil))
	record = accessRecord(t, &logs, "/api/folders")
	if record["status"] != float64(http.StatusUnauthorized) || record["bytes"] != float64(anonymous.Body.Len()) {
		t.Fatalf("anonymous access log = %v, want status 401 and %d bytes", record, anonymous.Body.Len())
	}
	if _, ok := record["user_id"]; ok {
		t.Fatalf("anonymous request logged a user_id: %v", record)
	}
}

func TestRequestIDHeaderAcceptsOnlySafeIncomingValues(t *testing.T) {
	var logs bytes.Buffer
	handler := httpapi.New(nil, nil, false, httpapi.WithLogger(bufferLogger(&logs, slog.LevelDebug)))
	tests := []struct {
		incoming string
		keep     bool
	}{
		{"", false},
		{"nginx-7f3a.b_c:1", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"with space", false},
		{"line\nbreak", false},
		{"<script>", false},
		{"-leading-dash", false},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, "/api/health/live", nil)
		if test.incoming != "" {
			request.Header["X-Request-Id"] = []string{test.incoming}
		}
		logs.Reset()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		got := response.Header().Get("X-Request-ID")
		if test.keep && got != test.incoming {
			t.Fatalf("incoming %q: X-Request-ID = %q, want it kept", test.incoming, got)
		}
		if !test.keep && !generatedRequestID.MatchString(got) {
			t.Fatalf("incoming %q: X-Request-ID = %q, want a generated ID", test.incoming, got)
		}
		if record := accessRecord(t, &logs, "/api/health/live"); record["request_id"] != got {
			t.Fatalf("logged request_id %v, header %q", record["request_id"], got)
		}
	}
}

func TestHealthChecksAreLoggedOnlyAtDebugLevel(t *testing.T) {
	var logs bytes.Buffer
	handler := httpapi.New(nil, nil, false, httpapi.WithLogger(bufferLogger(&logs, slog.LevelInfo)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/health/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/health/live = %d", response.Code)
	}
	if logs.Len() != 0 {
		t.Fatalf("health check logged at info level: %s", logs.String())
	}
	debugHandler := httpapi.New(nil, nil, false, httpapi.WithLogger(bufferLogger(&logs, slog.LevelDebug)))
	debugHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health/live", nil))
	if record := accessRecord(t, &logs, "/api/health/live"); record["level"] != "DEBUG" {
		t.Fatalf("health access log level = %v, want DEBUG", record["level"])
	}
}

func TestLivenessAndReadiness(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	for _, path := range []string{"/api/health/live", "/api/health/ready", "/api/health"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ok"`) {
			t.Fatalf("GET %s = %d %s, want 200 ok", path, response.Code, response.Body.String())
		}
	}
}

func TestReadinessFailsWhileLivenessSucceedsWithoutDatabase(t *testing.T) {
	// Nothing listens on port 1, so every connection attempt is refused at once.
	unreachable, err := pgxpool.New(context.Background(), "postgres://pulse:unused@127.0.0.1:1/pulse?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unreachable.Close)
	handler := httpapi.New(store.New(unreachable), syncer.NewControl(), false)
	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/api/health/live", nil))
	if live.Code != http.StatusOK {
		t.Fatalf("GET /api/health/live without database = %d, want 200", live.Code)
	}
	for _, path := range []string{"/api/health/ready", "/api/health"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		assertErrorCode(t, response, http.StatusServiceUnavailable, "system.database_unavailable")
	}
}
