package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/pulse-reader/app/internal/metrics"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

type Handler struct {
	store          *store.Store
	control        *syncer.Control
	secureCookie   bool
	trustedProxies []netip.Prefix
	now            func() time.Time
	loginLimiter   *loginLimiter
	logger         *slog.Logger
	metrics        *metrics.Metrics
}

// Option customizes a Handler created by New.
type Option func(*Handler)

// WithTrustedProxies sets the proxy networks whose X-Real-IP header is used as
// the client address for sign-in rate limiting. Without this option
// DefaultTrustedProxyCIDRs applies.
func WithTrustedProxies(prefixes []netip.Prefix) Option {
	return func(h *Handler) { h.trustedProxies = prefixes }
}

// WithClock replaces the clock used for sign-in rate limiting (for tests).
func WithClock(now func() time.Time) Option {
	return func(h *Handler) { h.now = now }
}

// WithLogger sets the logger for access logs and handler events. Without this
// option slog.Default() is used.
func WithLogger(logger *slog.Logger) Option {
	return func(h *Handler) { h.logger = logger }
}

// WithMetrics records request counts and durations by route pattern and sign-in
// outcomes. The metrics are not served by this handler; see metrics.Handler.
func WithMetrics(m *metrics.Metrics) Option {
	return func(h *Handler) { h.metrics = m }
}

func New(repository *store.Store, control *syncer.Control, secureCookie bool, options ...Option) http.Handler {
	handler := &Handler{store: repository, control: control, secureCookie: secureCookie, now: time.Now, logger: slog.Default()}
	handler.trustedProxies, _ = ParseTrustedProxyCIDRs(DefaultTrustedProxyCIDRs)
	for _, option := range options {
		option(handler)
	}
	handler.loginLimiter = newLoginLimiter(repository, handler.now, handler.logger)
	router := chi.NewRouter()
	// accessLog and instrument wrap Recoverer so a recovered panic is logged and
	// counted with status 500. markRouted must stay last: it marks requests that
	// no middleware rejected before routing.
	router.Use(requestID, handler.accessLog, handler.instrument, middleware.Recoverer, handler.sameOrigin, markRouted)
	router.Get("/api/health/live", handler.healthLive)
	router.Get("/api/health/ready", handler.healthReady)
	router.Get("/api/health", handler.healthReady) // compatibility alias of /api/health/ready
	router.Post("/api/auth/login", handler.login)
	router.Post("/api/auth/logout", handler.logout)
	router.With(handler.authenticate).Get("/api/auth/me", handler.me)
	router.With(handler.authenticate).Put("/api/auth/profile", handler.updateProfile)
	router.With(handler.authenticate).Post("/api/auth/password", handler.changePassword)
	router.With(handler.authenticate).Get("/api/folders", handler.listFolders)
	router.With(handler.authenticate).Post("/api/folders", handler.createFolder)
	router.With(handler.authenticate).Patch("/api/folders/{id}", handler.updateFolder)
	router.With(handler.authenticate).Delete("/api/folders/{id}", handler.deleteFolder)
	router.With(handler.authenticate).Get("/api/feeds", handler.listFeeds)
	router.With(handler.authenticate).Post("/api/feeds", handler.createFeed)
	router.With(handler.authenticate).Patch("/api/feeds/{id}", handler.updateFeed)
	router.With(handler.authenticate).Get("/api/articles", handler.listArticles)
	router.With(handler.authenticate).Post("/api/articles/mark-read", handler.markArticlesRead)
	router.With(handler.authenticate).Patch("/api/articles/{id}", handler.updateArticle)
	router.With(handler.authenticate).Get("/api/opml", handler.exportOPML)
	router.With(handler.authenticate).Post("/api/opml", handler.importOPML)
	router.With(handler.authenticate).Get("/api/refresh", handler.refreshStatus)
	router.With(handler.authenticate).Post("/api/refresh", handler.refreshFeeds)
	router.With(handler.authenticate, handler.requireAdministrator).Get("/api/admin/users", handler.listAdminUsers)
	router.With(handler.authenticate, handler.requireAdministrator).Post("/api/admin/users", handler.createAdminUser)
	router.With(handler.authenticate, handler.requireAdministrator).Patch("/api/admin/users/{id}", handler.updateAdminUser)
	router.With(handler.authenticate, handler.requireAdministrator).Post("/api/admin/users/{id}/sign-out", handler.signOutAdminUser)
	router.With(handler.authenticate, handler.requireAdministrator).Get("/api/admin/settings", handler.getAdminSettings)
	router.With(handler.authenticate, handler.requireAdministrator).Put("/api/admin/settings", handler.updateAdminSettings)
	router.With(handler.authenticate, handler.requireAdministrator).Get("/api/admin/feed-requests", handler.listFeedRequests)
	router.With(handler.authenticate, handler.requireAdministrator).Post("/api/admin/feed-requests/{id}/approve", handler.approveFeedRequest)
	router.With(handler.authenticate, handler.requireAdministrator).Delete("/api/admin/feed-requests/{id}", handler.rejectFeedRequest)
	router.With(handler.authenticate, handler.requireAdministrator).Get("/api/admin/stop-words", handler.listFeedStopWords)
	router.With(handler.authenticate, handler.requireAdministrator).Post("/api/admin/stop-words", handler.addFeedStopWord)
	router.With(handler.authenticate, handler.requireAdministrator).Delete("/api/admin/stop-words/{id}", handler.deleteFeedStopWord)
	router.With(handler.authenticate, handler.requireAdministrator).Get("/api/admin/blocked-articles", handler.listBlockedFeedArticles)
	router.With(handler.authenticate, handler.requireAdministrator).Post("/api/admin/blocked-articles/{id}/allow", handler.allowBlockedFeedArticle)
	router.With(handler.authenticate, handler.requireAdministrator).Post("/api/admin/blocked-articles/{id}/reject", handler.rejectBlockedFeedArticle)
	return router
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	var body json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "request.invalid_json", "Check the format of the submitted data.")
		return false
	}
	// Every API payload is an object. In particular, decoding null into a
	// struct succeeds without setting fields and can silently clear a profile.
	if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || trimmed[0] != '{' {
		writeError(w, http.StatusBadRequest, "request.invalid_json", "Check the format of the submitted data.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request.multiple_json_values", "The request must contain exactly one JSON value.")
		return false
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(body))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "request.invalid_json", "Check the format of the submitted data.")
		return false
	}
	return true
}

func optionalID(raw string) (*int64, bool) {
	if raw == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return nil, false
	}
	return &id, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}
