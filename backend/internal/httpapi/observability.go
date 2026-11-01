package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const requestIDHeader = "X-Request-ID"

// incomingRequestID limits which client- or proxy-supplied request IDs are
// reused: short tokens without spaces, control characters or markup, so the
// value is safe in log lines and response headers.
var incomingRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// requestID reuses a well-formed X-Request-ID header or generates a new ID,
// stores it where chi's middleware.GetReqID finds it, and returns it in the
// X-Request-ID response header.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !incomingRequestID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), middleware.RequestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:]) // crypto/rand.Read never returns an error.
	return hex.EncodeToString(raw[:])
}

// requestLogFields collects values that inner handlers learn while serving a
// request. accessLog creates it before authenticate runs, and authenticate
// records the signed-in user in it.
type requestLogFields struct {
	userID int64
}

type requestLogFieldsKey struct{}

func setLoggedUser(r *http.Request, userID int64) {
	if fields, ok := r.Context().Value(requestLogFieldsKey{}).(*requestLogFields); ok {
		fields.userID = userID
	}
}

// accessLog writes one record per request with method, path, status, response
// size, duration, request ID and the authenticated user. The query string is
// never logged because it can contain search terms. Health checks are logged
// at debug level so probes do not flood the log.
func (h *Handler) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		fields := &requestLogFields{}
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r.WithContext(context.WithValue(r.Context(), requestLogFieldsKey{}, fields)))

		level := slog.LevelInfo
		if r.URL.Path == "/api/health" || strings.HasPrefix(r.URL.Path, "/api/health/") {
			level = slog.LevelDebug
		}
		if !h.logger.Enabled(r.Context(), level) {
			return
		}
		status := wrapped.Status()
		if status == 0 {
			// The handler wrote nothing; net/http sends 200.
			status = http.StatusOK
		}
		attributes := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Int("bytes", wrapped.BytesWritten()),
			slog.Float64("duration_ms", math.Round(float64(time.Since(started).Microseconds()))/1000),
			slog.String("request_id", middleware.GetReqID(r.Context())),
		}
		if fields.userID != 0 {
			attributes = append(attributes, slog.Int64("user_id", fields.userID))
		}
		h.logger.LogAttrs(r.Context(), level, "http request", attributes...)
	})
}

// instrument records each request in the metrics under its chi route pattern (for
// example /api/feeds/{id}), never the raw path, so IDs do not become label values.
func (h *Handler) instrument(next http.Handler) http.Handler {
	if h.metrics == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		route := ""
		if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
			route = routeContext.RoutePattern()
		}
		h.metrics.ObserveHTTPRequest(route, r.Method, wrapped.Status(), time.Since(started))
	})
}

// healthLive reports that the process is up. It does not touch the database.
func (h *Handler) healthLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// healthReady reports whether the API can serve requests, which requires the database.
func (h *Handler) healthReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		h.logger.WarnContext(r.Context(), "readiness check failed", slog.String("error", err.Error()), slog.String("request_id", middleware.GetReqID(r.Context())))
		writeError(w, http.StatusServiceUnavailable, "system.database_unavailable", "The database is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

const readinessTimeout = 2 * time.Second
