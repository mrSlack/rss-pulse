package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

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
