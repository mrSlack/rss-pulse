package httpapi

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pulse-reader/app/internal/store"
)

// markArticlesRead handles POST /api/articles/mark-read. It accepts the same
// filter as GET /api/articles plus at least one bound, upTo (publication
// time) or upToId (the latestArticleId of the loaded list), and marks every
// matching article within the bounds as read.
func (h *Handler) markArticlesRead(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Filter   string `json:"filter"`
		FeedID   *int64 `json:"feedId"`
		FolderID *int64 `json:"folderId"`
		Query    string `json:"q"`
		Since    string `json:"since"`
		UpTo     string `json:"upTo"`
		UpToID   *int64 `json:"upToId"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Filter == "" {
		request.Filter = "all"
	}
	if request.Filter != "all" && request.Filter != "unread" && request.Filter != "today" && request.Filter != "saved" {
		writeError(w, http.StatusBadRequest, "article.filter_invalid", "The article filter is not recognized.")
		return
	}
	if request.FeedID != nil && *request.FeedID < 1 {
		writeError(w, http.StatusBadRequest, "feed.id_invalid", "The feed ID is invalid.")
		return
	}
	if request.FolderID != nil && *request.FolderID < 1 {
		writeError(w, http.StatusBadRequest, "folder.id_invalid", "The folder ID is invalid.")
		return
	}
	search := strings.TrimSpace(request.Query)
	if utf8.RuneCountInString(search) > 200 {
		writeError(w, http.StatusBadRequest, "article.search_too_long", "The search query is too long.")
		return
	}
	var since *time.Time
	if request.Since != "" {
		parsed, err := time.Parse(time.RFC3339, request.Since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "article.date_invalid", "The filter date is invalid.")
			return
		}
		since = &parsed
	}
	if request.UpToID != nil && *request.UpToID < 0 {
		writeError(w, http.StatusBadRequest, "article.id_invalid", "The article ID is invalid.")
		return
	}
	bound := store.MarkReadBound{ArticleID: request.UpToID}
	// Older clients send only upTo; it stays required unless upToId is given.
	if request.UpTo != "" || request.UpToID == nil {
		upTo, err := time.Parse(time.RFC3339, request.UpTo)
		if err != nil {
			writeError(w, http.StatusBadRequest, "article.date_invalid", "The filter date is invalid.")
			return
		}
		bound.PublishedAt = &upTo
	}
	user, _ := currentUser(r)
	marked, err := h.store.MarkArticlesReadForUser(r.Context(), user.ID, store.ArticleFilter{
		Filter: request.Filter, FeedID: request.FeedID, FolderID: request.FolderID, Query: search, Since: since,
	}, bound)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "article.mark_read_failed", "The articles could not be marked as read.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"marked": marked})
}
