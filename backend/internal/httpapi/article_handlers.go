package httpapi

import (
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
	_, hasOffset := query["offset"]
	_, hasBefore := query["before"]
	if hasOffset && hasBefore {
		writeError(w, http.StatusBadRequest, "article.pagination_conflict", "Use either a page cursor or an offset, not both.")
		return
	}
	var before *store.ArticleCursor
	if hasBefore {
		cursor, err := store.DecodeArticleCursor(query.Get("before"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "article.cursor_invalid", "The page cursor is invalid.")
			return
		}
		before = &cursor
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
	// Read before the list so the bound never covers an article the list could
	// not show; one ingested in between is listed but stays outside the bound.
	latestID, err := h.store.LatestArticleID(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "article.load_failed", "Articles could not be loaded.")
		return
	}
	articles, err := h.store.ListArticlesForUser(r.Context(), user.ID, filter, feedID, folderID, search, since, limit, offset, before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "article.load_failed", "Articles could not be loaded.")
		return
	}
	hasMore := len(articles) > limit
	var nextCursor *string
	if hasMore {
		articles = articles[:limit]
		last := articles[len(articles)-1]
		encoded := store.EncodeArticleCursor(store.ArticleCursor{PublishedAt: last.PublishedAt, ID: last.ID})
		nextCursor = &encoded
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": articles, "hasMore": hasMore, "nextCursor": nextCursor, "latestArticleId": latestID})
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
	if err != nil {
		writeStoreError(w, err, []storeError{{store.ErrNotFound, apiError{http.StatusNotFound, "article.not_found", "The article was not found."}}},
			apiError{http.StatusInternalServerError, "article.update_failed", "The article could not be updated."})
		return
	}
	writeJSON(w, http.StatusOK, article)
}

func (h *Handler) refreshFeeds(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	// A user who is already queued or refreshing is covered by that request.
	h.control.RequestUser(user.ID)
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Feed refresh started."})
}

func (h *Handler) refreshStatus(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	writeJSON(w, http.StatusOK, map[string]bool{"running": h.control.IsUserRefreshing(user.ID)})
}
