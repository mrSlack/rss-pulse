package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

type Handler struct {
	store        *store.Store
	control      *syncer.Control
	secureCookie bool
}

func New(repository *store.Store, control *syncer.Control, secureCookie bool) http.Handler {
	handler := &Handler{store: repository, control: control, secureCookie: secureCookie}
	router := chi.NewRouter()
	router.Use(middleware.RequestID, middleware.Recoverer, handler.sameOrigin)
	router.Get("/api/health", handler.health)
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
	router.With(handler.authenticate).Patch("/api/articles/{id}", handler.updateArticle)
	router.With(handler.authenticate).Get("/api/refresh", handler.refreshStatus)
	router.With(handler.authenticate).Post("/api/refresh", handler.refreshFeeds)
	router.With(handler.authenticate, handler.requireAdministrator).Get("/api/admin/users", handler.listAdminUsers)
	router.With(handler.authenticate, handler.requireAdministrator).Post("/api/admin/users", handler.createAdminUser)
	router.With(handler.authenticate, handler.requireAdministrator).Patch("/api/admin/users/{id}", handler.updateAdminUser)
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

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "system.database_unavailable", "The database is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

func timeBoundContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 15*time.Second)
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
