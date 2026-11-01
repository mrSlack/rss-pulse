package httpapi

import (
	"errors"
	"net/http"
)

// apiError is one HTTP error response: status, stable code, and English fallback message.
type apiError struct {
	status  int
	code    string
	message string
}

func (e apiError) write(w http.ResponseWriter) {
	writeError(w, e.status, e.code, e.message)
}

// storeError maps one store sentinel error to the response a handler sends for it.
type storeError struct {
	err      error
	response apiError
}

// writeStoreError writes the response of the first case whose error matches err
// (errors.Is), or fallback when none matches. Cases are checked in order, like
// the if-ladders they replace.
func writeStoreError(w http.ResponseWriter, err error, cases []storeError, fallback apiError) {
	for _, c := range cases {
		if errors.Is(err, c.err) {
			c.response.write(w)
			return
		}
	}
	fallback.write(w)
}

// Responses shared by several handlers.
var (
	errFolderNameTaken           = apiError{http.StatusConflict, "folder.name_taken", "A folder with this name already exists."}
	errFolderNotFound            = apiError{http.StatusNotFound, "folder.not_found", "The folder could not be found."}
	errFolderUnavailable         = apiError{http.StatusBadRequest, "folder.unavailable", "The selected folder is unavailable."}
	errFeedAlreadyAdded          = apiError{http.StatusConflict, "feed.already_added", "This feed has already been added."}
	errFeedLimitReached          = apiError{http.StatusConflict, "feed.limit_reached", "You have reached the maximum of 100 feeds."}
	errFeedURLInvalid            = apiError{http.StatusBadRequest, "feed.url_invalid", "Enter a valid HTTP or HTTPS feed URL."}
	errFeedNotFound              = apiError{http.StatusNotFound, "feed.not_found", "The feed could not be found."}
	errFeedRequestLimitReached   = apiError{http.StatusConflict, "feed.request_limit_reached", "You already have 10 feed requests waiting for approval. Wait until an administrator reviews them."}
	errFeedSaveFailed            = apiError{http.StatusInternalServerError, "feed.save_failed", "The feed could not be saved."}
	errFeedUpdateFailed          = apiError{http.StatusInternalServerError, "feed.update_failed", "The feed could not be updated."}
	errUserNotFound              = apiError{http.StatusNotFound, "user.not_found", "The user was not found."}
	errFeedRequestNotFound       = apiError{http.StatusNotFound, "feed.request_not_found", "The feed request could not be found."}
	errBlockedArticleUnavailable = apiError{http.StatusNotFound, "admin.blocked_article_not_found", "The blocked article was not found or is unavailable for moderation."}
)
