package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/pulse-reader/app/internal/store"
)

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
