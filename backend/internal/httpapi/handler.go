package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/pulse-reader/app/internal/feeds"
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
	return router
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "system.database_unavailable", "The database is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) listFolders(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	folders, err := h.store.ListFoldersForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "folder.load_failed", "Folders could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, folders)
}

func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		writeError(w, http.StatusBadRequest, "folder.name_invalid", "Folder names must contain between 1 and 60 characters.")
		return
	}
	if request.Color == "" {
		request.Color = store.DefaultFolderColor
	}
	if strings.HasPrefix(request.Color, "#") {
		request.Color = strings.ToLower(request.Color)
	}
	if !store.IsValidFolderColor(request.Color) {
		writeError(w, http.StatusBadRequest, "folder.color_invalid", "Choose a preset color or a six-digit hexadecimal color.")
		return
	}
	user, _ := currentUser(r)
	folder, err := h.store.CreateFolderForUser(r.Context(), user.ID, name, request.Color)
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "folder.name_taken", "A folder with this name already exists.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "folder.create_failed", "The folder could not be created.")
		return
	}
	writeJSON(w, http.StatusCreated, folder)
}

func (h *Handler) updateFolder(w http.ResponseWriter, r *http.Request) {
	folderID, ok := optionalID(chi.URLParam(r, "id"))
	if !ok || folderID == nil {
		writeError(w, http.StatusBadRequest, "folder.id_invalid", "The folder ID is invalid.")
		return
	}
	var request struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		writeError(w, http.StatusBadRequest, "folder.name_invalid", "Folder names must contain between 1 and 60 characters.")
		return
	}
	if strings.HasPrefix(request.Color, "#") {
		request.Color = strings.ToLower(request.Color)
	}
	if !store.IsValidFolderColor(request.Color) {
		writeError(w, http.StatusBadRequest, "folder.color_invalid", "Choose a preset color or a six-digit hexadecimal color.")
		return
	}
	user, _ := currentUser(r)
	folder, err := h.store.UpdateFolderForUser(r.Context(), user.ID, *folderID, name, request.Color)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "folder.not_found", "The folder could not be found.")
		return
	}
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "folder.name_taken", "A folder with this name already exists.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "folder.update_failed", "The folder could not be updated.")
		return
	}
	writeJSON(w, http.StatusOK, folder)
}

func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request) {
	folderID, ok := optionalID(chi.URLParam(r, "id"))
	if !ok || folderID == nil {
		writeError(w, http.StatusBadRequest, "folder.id_invalid", "The folder ID is invalid.")
		return
	}
	var request struct {
		Action         string `json:"action"`
		TargetFolderID *int64 `json:"targetFolderId"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	user, _ := currentUser(r)
	err := h.store.DeleteFolderForUser(r.Context(), user.ID, *folderID, request.Action, request.TargetFolderID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "folder.not_found", "The folder could not be found.")
	case errors.Is(err, store.ErrFolderUnavailable):
		writeError(w, http.StatusBadRequest, "folder.target_unavailable", "Choose another folder owned by your account.")
	case errors.Is(err, store.ErrInvalidFolderOperation):
		writeError(w, http.StatusBadRequest, "folder.delete_action_invalid", "Choose whether to remove the sources or move them to another folder.")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "folder.delete_failed", "The folder could not be deleted.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) listFeeds(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	feedsList, err := h.store.ListFeedsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "feed.load_failed", "Feeds could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, feedsList)
}

func (h *Handler) createFeed(w http.ResponseWriter, r *http.Request) {
	var request struct {
		URL      string `json:"url"`
		FolderID *int64 `json:"folderId"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.URL = strings.TrimSpace(request.URL)
	if len(request.URL) > 2048 {
		writeError(w, http.StatusBadRequest, "feed.url_too_long", "The feed URL is too long.")
		return
	}
	if request.URL == "" {
		writeError(w, http.StatusBadRequest, "feed.url_required", "Enter a feed URL.")
		return
	}
	user, _ := currentUser(r)
	result, err := h.store.RequestFeedApproval(r.Context(), user.ID, request.URL, "", request.FolderID, nil)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeError(w, http.StatusConflict, "feed.already_added", "This feed has already been added.")
			return
		}
		if errors.Is(err, store.ErrFeedLimitReached) {
			writeError(w, http.StatusConflict, "feed.limit_reached", "You have reached the maximum of 100 feeds.")
			return
		}
		if errors.Is(err, store.ErrFolderUnavailable) {
			writeError(w, http.StatusBadRequest, "folder.unavailable", "The selected folder is unavailable.")
			return
		}
		if errors.Is(err, store.ErrInvalidFeedURL) {
			writeError(w, http.StatusBadRequest, "feed.url_invalid", "Enter a valid HTTP or HTTPS feed URL.")
			return
		}
		if errors.Is(err, store.ErrFeedRequestPending) {
			writeError(w, http.StatusConflict, "feed.request_pending", "A request for this feed is already pending.")
			return
		}
		writeError(w, http.StatusInternalServerError, "feed.save_failed", "The feed could not be saved.")
		return
	}
	if !result.Approved {
		writeJSON(w, http.StatusAccepted, map[string]any{"requestId": result.RequestID, "status": "pending"})
		return
	}
	feed, err := h.store.SubscribeSource(r.Context(), user.ID, result.SourceID, request.FolderID)
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "feed.already_added", "This feed has already been added.")
		return
	}
	if errors.Is(err, store.ErrFeedLimitReached) {
		writeError(w, http.StatusConflict, "feed.limit_reached", "You have reached the maximum of 100 feeds.")
		return
	}
	if err != nil {
		if errors.Is(err, store.ErrFolderUnavailable) {
			writeError(w, http.StatusBadRequest, "folder.unavailable", "The selected folder is unavailable.")
			return
		}
		writeError(w, http.StatusInternalServerError, "feed.save_failed", "The feed could not be saved.")
		return
	}
	writeJSON(w, http.StatusCreated, feed)
}

func (h *Handler) updateFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := optionalID(chi.URLParam(r, "id"))
	if !ok || feedID == nil {
		writeError(w, http.StatusBadRequest, "feed.id_invalid", "The feed ID is invalid.")
		return
	}
	var request struct {
		Title    string `json:"title"`
		URL      string `json:"url"`
		FolderID *int64 `json:"folderId"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Title = strings.TrimSpace(request.Title)
	request.URL = strings.TrimSpace(request.URL)
	if request.Title == "" || utf8.RuneCountInString(request.Title) > 120 {
		writeError(w, http.StatusBadRequest, "feed.title_invalid", "Feed names must contain between 1 and 120 characters.")
		return
	}
	if len(request.URL) > 2048 {
		writeError(w, http.StatusBadRequest, "feed.url_too_long", "The feed URL is too long.")
		return
	}
	if request.URL == "" {
		writeError(w, http.StatusBadRequest, "feed.url_required", "Enter a feed URL.")
		return
	}
	user, _ := currentUser(r)
	result, err := h.store.RequestFeedApproval(r.Context(), user.ID, request.URL, request.Title, request.FolderID, feedID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "feed.not_found", "The feed could not be found.")
		return
	}
	if errors.Is(err, store.ErrFeedRequestPending) {
		writeError(w, http.StatusConflict, "feed.request_pending", "A request for this feed is already pending.")
		return
	}
	if errors.Is(err, store.ErrFolderUnavailable) {
		writeError(w, http.StatusBadRequest, "folder.unavailable", "The selected folder is unavailable.")
		return
	}
	if errors.Is(err, store.ErrInvalidFeedURL) {
		writeError(w, http.StatusBadRequest, "feed.url_invalid", "Enter a valid HTTP or HTTPS feed URL.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "feed.update_failed", "The feed could not be updated.")
		return
	}
	if !result.Approved {
		writeJSON(w, http.StatusAccepted, map[string]any{"requestId": result.RequestID, "status": "pending"})
		return
	}
	sourceID, sourceCreated, urlChanged, err := h.store.PrepareFeedUpdate(r.Context(), user.ID, *feedID, request.URL, request.FolderID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "feed.not_found", "The feed could not be found.")
		return
	}
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "feed.already_added", "This feed has already been added.")
		return
	}
	if errors.Is(err, store.ErrFolderUnavailable) {
		writeError(w, http.StatusBadRequest, "folder.unavailable", "The selected folder is unavailable.")
		return
	}
	if errors.Is(err, store.ErrInvalidFeedURL) {
		writeError(w, http.StatusBadRequest, "feed.url_invalid", "Enter a valid HTTP or HTTPS feed URL.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "feed.update_failed", "The feed could not be updated.")
		return
	}
	if urlChanged {
		lease, claimed, err := h.store.ClaimSource(r.Context(), sourceID, nil)
		if err != nil {
			if sourceCreated {
				_ = h.store.DiscardEmptySource(r.Context(), sourceID)
			}
			writeError(w, http.StatusInternalServerError, "feed.refresh_claim_failed", "The feed could not be claimed for refresh.")
			return
		}
		if claimed {
			feedContext, cancel := timeBoundContext(r)
			document, fetchErr := feeds.Fetch(feedContext, lease.URL)
			cancel()
			if fetchErr != nil {
				_ = h.store.ReleaseSourceLease(r.Context(), lease, fetchErr.Error())
				if sourceCreated {
					_ = h.store.DiscardEmptySource(r.Context(), sourceID)
				}
				writeError(w, http.StatusUnprocessableEntity, "feed.fetch_failed", "The feed could not be fetched. Check the URL and try again.")
				return
			}
			if err := h.store.CommitSourceRefresh(r.Context(), lease, document); err != nil {
				_ = h.store.ReleaseSourceLease(r.Context(), lease, err.Error())
				if sourceCreated {
					_ = h.store.DiscardEmptySource(r.Context(), sourceID)
				}
				writeError(w, http.StatusInternalServerError, "feed.articles_save_failed", "Feed articles could not be saved.")
				return
			}
		}
	}
	feed, err := h.store.UpdateFeedForUser(r.Context(), user.ID, *feedID, sourceID, request.Title, request.FolderID)
	if errors.Is(err, store.ErrNotFound) {
		if sourceCreated {
			_ = h.store.DiscardEmptySource(r.Context(), sourceID)
		}
		writeError(w, http.StatusNotFound, "feed.not_found", "The feed could not be found.")
		return
	}
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "feed.already_added", "This feed has already been added.")
		return
	}
	if errors.Is(err, store.ErrFolderUnavailable) {
		writeError(w, http.StatusBadRequest, "folder.unavailable", "The selected folder is unavailable.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "feed.update_failed", "The feed could not be updated.")
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (h *Handler) listArticles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := query.Get("filter")
	if filter == "" {
		filter = "all"
	}
	if filter != "all" && filter != "unread" && filter != "today" && filter != "saved" {
		writeError(w, http.StatusBadRequest, "article.filter_invalid", "The article filter is not recognized.")
		return
	}
	feedID, ok := optionalID(query.Get("feedId"))
	if !ok {
		writeError(w, http.StatusBadRequest, "feed.id_invalid", "The feed ID is invalid.")
		return
	}
	folderID, ok := optionalID(query.Get("folderId"))
	if !ok {
		writeError(w, http.StatusBadRequest, "folder.id_invalid", "The folder ID is invalid.")
		return
	}
	search := strings.TrimSpace(query.Get("q"))
	if utf8.RuneCountInString(search) > 200 {
		writeError(w, http.StatusBadRequest, "article.search_too_long", "The search query is too long.")
		return
	}
	var since *time.Time
	if rawSince := query.Get("since"); rawSince != "" {
		parsed, err := time.Parse(time.RFC3339, rawSince)
		if err != nil {
			writeError(w, http.StatusBadRequest, "article.date_invalid", "The filter date is invalid.")
			return
		}
		since = &parsed
	}
	limit := 20
	if rawLimit := query.Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, http.StatusBadRequest, "article.limit_invalid", "The article limit must be between 1 and 200.")
			return
		}
		limit = parsed
	}
	offset := 0
	if rawOffset := query.Get("offset"); rawOffset != "" {
		parsed, err := strconv.Atoi(rawOffset)
		if err != nil || parsed < 0 || parsed > 100000 {
			writeError(w, http.StatusBadRequest, "article.offset_invalid", "The article offset is invalid.")
			return
		}
		offset = parsed
	}
	user, _ := currentUser(r)
	articles, err := h.store.ListArticlesForUser(r.Context(), user.ID, filter, feedID, folderID, search, since, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "article.load_failed", "Articles could not be loaded.")
		return
	}
	hasMore := len(articles) > limit
	if hasMore {
		articles = articles[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": articles, "hasMore": hasMore})
}

func (h *Handler) updateArticle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "article.id_invalid", "The article ID is invalid.")
		return
	}
	var update struct {
		IsRead  *bool `json:"isRead"`
		IsSaved *bool `json:"isSaved"`
	}
	if !decodeJSON(w, r, &update) {
		return
	}
	if update.IsRead == nil && update.IsSaved == nil {
		writeError(w, http.StatusBadRequest, "article.update_missing", "Specify an article status to update.")
		return
	}
	user, _ := currentUser(r)
	article, err := h.store.UpdateArticleForUser(r.Context(), user.ID, id, update.IsRead, update.IsSaved)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "article.not_found", "The article was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "article.update_failed", "The article could not be updated.")
		return
	}
	writeJSON(w, http.StatusOK, article)
}

func (h *Handler) refreshFeeds(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	if !h.control.RequestUser(user.ID) {
		writeError(w, http.StatusConflict, "feed.refresh_busy", "A feed refresh is already running or queued.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Feed refresh started."})
}

func (h *Handler) refreshStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"running": h.control.IsRunning()})
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
