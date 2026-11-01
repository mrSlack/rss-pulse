package httpapi

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
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
	if err != nil {
		writeStoreError(w, err, []storeError{{store.ErrDuplicate, errFolderNameTaken}},
			apiError{http.StatusInternalServerError, "folder.create_failed", "The folder could not be created."})
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
	if err != nil {
		writeStoreError(w, err, []storeError{{store.ErrNotFound, errFolderNotFound}, {store.ErrDuplicate, errFolderNameTaken}},
			apiError{http.StatusInternalServerError, "folder.update_failed", "The folder could not be updated."})
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
	if err := h.store.DeleteFolderForUser(r.Context(), user.ID, *folderID, request.Action, request.TargetFolderID); err != nil {
		writeStoreError(w, err, []storeError{
			{store.ErrNotFound, errFolderNotFound},
			{store.ErrFolderUnavailable, apiError{http.StatusBadRequest, "folder.target_unavailable", "Choose another folder owned by your account."}},
			{store.ErrInvalidFolderOperation, apiError{http.StatusBadRequest, "folder.delete_action_invalid", "Choose whether to remove the sources or move them to another folder."}},
		}, apiError{http.StatusInternalServerError, "folder.delete_failed", "The folder could not be deleted."})
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		writeStoreError(w, err, []storeError{
			{store.ErrDuplicate, errFeedAlreadyAdded},
			{store.ErrFeedLimitReached, errFeedLimitReached},
			{store.ErrFolderUnavailable, errFolderUnavailable},
			{store.ErrInvalidFeedURL, errFeedURLInvalid},
			{store.ErrFeedRequestLimitReached, errFeedRequestLimitReached},
		}, errFeedSaveFailed)
		return
	}
	if !result.Approved {
		writeJSON(w, http.StatusAccepted, map[string]any{"requestId": result.RequestID, "status": "pending"})
		return
	}
	feed, err := h.store.SubscribeSource(r.Context(), user.ID, result.SourceID, request.FolderID)
	if err != nil {
		writeStoreError(w, err, []storeError{
			{store.ErrDuplicate, errFeedAlreadyAdded},
			{store.ErrFeedLimitReached, errFeedLimitReached},
			{store.ErrFolderUnavailable, errFolderUnavailable},
		}, errFeedSaveFailed)
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
	if err != nil {
		writeStoreError(w, err, []storeError{
			{store.ErrNotFound, errFeedNotFound},
			{store.ErrFeedRequestLimitReached, errFeedRequestLimitReached},
			{store.ErrFolderUnavailable, errFolderUnavailable},
			{store.ErrInvalidFeedURL, errFeedURLInvalid},
		}, errFeedUpdateFailed)
		return
	}
	if !result.Approved {
		writeJSON(w, http.StatusAccepted, map[string]any{"requestId": result.RequestID, "status": "pending"})
		return
	}
	sourceID, urlChanged, err := h.store.PrepareFeedUpdate(r.Context(), user.ID, *feedID, request.URL, request.FolderID)
	if err != nil {
		writeStoreError(w, err, []storeError{
			{store.ErrNotFound, errFeedNotFound},
			{store.ErrDuplicate, errFeedAlreadyAdded},
			{store.ErrFolderUnavailable, errFolderUnavailable},
			{store.ErrInvalidFeedURL, errFeedURLInvalid},
		}, errFeedUpdateFailed)
		return
	}
	feed, err := h.store.UpdateFeedForUser(r.Context(), user.ID, *feedID, sourceID, request.Title, request.FolderID)
	if err != nil {
		writeStoreError(w, err, []storeError{
			{store.ErrNotFound, errFeedNotFound},
			{store.ErrDuplicate, errFeedAlreadyAdded},
			{store.ErrFolderUnavailable, errFolderUnavailable},
		}, errFeedUpdateFailed)
		return
	}
	if urlChanged && h.control != nil {
		// The new source is fetched in the background; GET /api/refresh reports
		// the user's refresh as running until it has been processed.
		h.control.RequestUser(user.ID)
	}
	writeJSON(w, http.StatusOK, feed)
}
