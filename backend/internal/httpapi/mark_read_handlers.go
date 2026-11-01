package httpapi

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pulse-reader/app/internal/store"
)

// markArticlesRead handles POST /api/articles/mark-read. It accepts the same
// filter as GET /api/articles plus a required upTo bound and marks every
// matching article published at or before upTo as read.
func (h *Handler) markArticlesRead(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Filter   string `json:"filter"`
		FeedID   *int64 `json:"feedId"`
		FolderID *int64 `json:"folderId"`
		Query    string `json:"q"`
		Since    string `json:"since"`
		UpTo     string `json:"upTo"`
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
	upTo, err := time.Parse(time.RFC3339, request.UpTo)
	if err != nil {
		writeError(w, http.StatusBadRequest, "article.date_invalid", "The filter date is invalid.")
		return
	}
	user, _ := currentUser(r)
	marked, err := h.store.MarkArticlesReadForUser(r.Context(), user.ID, store.ArticleFilter{
		Filter: request.Filter, FeedID: request.FeedID, FolderID: request.FolderID, Query: search, Since: since,
	}, upTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "article.mark_read_failed", "The articles could not be marked as read.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"marked": marked})
}
