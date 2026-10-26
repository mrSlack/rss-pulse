package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/store"
)

func (h *Handler) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.users_load_failed", "Users could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *Handler) createAdminUser(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	if !validEmail(request.Email) || len([]rune(request.Password)) < 12 || !validRole(request.Role) {
		writeError(w, http.StatusBadRequest, "admin.user_input_invalid", "Check the email address, password length (at least 12 characters), and selected role.")
		return
	}
	passwordHash, err := auth.HashPassword(request.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.user_create_failed", "The user could not be created.")
		return
	}
	user, err := h.store.CreateUser(r.Context(), request.Email, passwordHash, request.Role)
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "admin.email_already_exists", "A user with this email address already exists.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.user_create_failed", "The user could not be created.")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (h *Handler) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || userID < 1 {
		writeError(w, http.StatusBadRequest, "admin.user_id_invalid", "The user ID is invalid.")
		return
	}
	var request map[string]json.RawMessage
	if !decodeJSON(w, r, &request) {
		return
	}
	var role *string
	if rawRole, exists := request["role"]; exists {
		var value string
		if err := json.Unmarshal(rawRole, &value); err != nil || !validRole(value) {
			writeError(w, http.StatusBadRequest, "admin.role_invalid", "The role must be Administrator or Reader.")
			return
		}
		role = &value
	}
	var interval *int
	updateInterval := false
	if rawInterval, exists := request["refreshIntervalMinutes"]; exists {
		updateInterval = true
		if string(rawInterval) != "null" {
			var value int
			if err := json.Unmarshal(rawInterval, &value); err != nil || value < 5 || value > 1440 {
				writeError(w, http.StatusBadRequest, "admin.user_interval_invalid", "The interval must be between 5 and 1,440 minutes, or inherit the default.")
				return
			}
			interval = &value
		}
	}
	for key := range request {
		if key != "role" && key != "refreshIntervalMinutes" {
			writeError(w, http.StatusBadRequest, "admin.user_field_unknown", "The user field is not recognized.")
			return
		}
	}
	if role == nil && !updateInterval {
		writeError(w, http.StatusBadRequest, "admin.user_update_missing", "Specify a role or refresh interval.")
		return
	}
	if err := h.store.UpdateUserAdmin(r.Context(), userID, role, updateInterval, interval); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user.not_found", "The user was not found.")
		return
	} else if err != nil {
		if errors.Is(err, store.ErrLastAdministrator) {
			writeError(w, http.StatusConflict, "admin.last_administrator", "The last Administrator cannot be demoted. The system must always have at least one Administrator.")
		} else {
			writeError(w, http.StatusInternalServerError, "admin.user_save_failed", "The user could not be saved.")
		}
		return
	}
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.user_load_failed", "The user could not be loaded.")
		return
	}
	for _, user := range users {
		if user.ID == userID {
			writeJSON(w, http.StatusOK, user)
			return
		}
	}
	writeError(w, http.StatusNotFound, "user.not_found", "The user was not found.")
}

func (h *Handler) getAdminSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.store.GetAdminSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.settings_load_failed", "Settings could not be loaded.")
		return
	}
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.users_load_failed", "Users could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"refreshIntervalMinutes": settings.RefreshIntervalMinutes,
		"sharedFeedTTLMinutes":   settings.SharedFeedTTLMinutes,
		"users":                  users,
	})
}

func (h *Handler) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
	var settings store.AdminSettings
	if !decodeJSON(w, r, &settings) {
		return
	}
	if settings.RefreshIntervalMinutes < 5 || settings.RefreshIntervalMinutes > 1440 || settings.SharedFeedTTLMinutes < 5 || settings.SharedFeedTTLMinutes > 1440 {
		writeError(w, http.StatusBadRequest, "admin.intervals_invalid", "Intervals must be between 5 and 1,440 minutes.")
		return
	}
	if err := h.store.UpdateAdminSettings(r.Context(), settings); err != nil {
		writeError(w, http.StatusInternalServerError, "admin.settings_save_failed", "Settings could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) listFeedRequests(w http.ResponseWriter, r *http.Request) {
	requests, err := h.store.ListPendingFeedRequests(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "feed.requests_load_failed", "Feed requests could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, requests)
}

func (h *Handler) approveFeedRequest(w http.ResponseWriter, r *http.Request) {
	requestID, ok := optionalID(chi.URLParam(r, "id"))
	if !ok || requestID == nil {
		writeError(w, http.StatusBadRequest, "feed.request_id_invalid", "The feed request ID is invalid.")
		return
	}
	if _, err := h.store.ApproveFeedRequest(r.Context(), *requestID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "feed.request_not_found", "The feed request could not be found.")
		return
	} else if errors.Is(err, store.ErrFeedLimitReached) {
		writeError(w, http.StatusConflict, "feed.limit_reached", "The requester has reached the maximum of 100 feeds.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "feed.request_approve_failed", "The feed request could not be approved.")
		return
	}
	if h.control != nil {
		h.control.Request(true)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) rejectFeedRequest(w http.ResponseWriter, r *http.Request) {
	requestID, ok := optionalID(chi.URLParam(r, "id"))
	if !ok || requestID == nil {
		writeError(w, http.StatusBadRequest, "feed.request_id_invalid", "The feed request ID is invalid.")
		return
	}
	if err := h.store.RejectFeedRequest(r.Context(), *requestID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "feed.request_not_found", "The feed request could not be found.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "feed.request_reject_failed", "The feed request could not be rejected.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listFeedStopWords(w http.ResponseWriter, r *http.Request) {
	words, err := h.store.ListStopWords(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.stop_words_load_failed", "The stop-word dictionary could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, words)
}

func (h *Handler) addFeedStopWord(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Term string `json:"term"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	word, err := h.store.AddStopWord(r.Context(), request.Term)
	if errors.Is(err, store.ErrInvalidStopWord) {
		writeError(w, http.StatusBadRequest, "admin.stop_word_invalid", "A stop word or phrase must contain 1 to 80 letters or digits.")
		return
	}
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "admin.stop_word_exists", "That stop word or phrase is already in the dictionary.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.stop_word_add_failed", "The stop word could not be added.")
		return
	}
	writeJSON(w, http.StatusCreated, word)
}

func (h *Handler) deleteFeedStopWord(w http.ResponseWriter, r *http.Request) {
	id, ok := optionalID(chi.URLParam(r, "id"))
	if !ok || id == nil {
		writeError(w, http.StatusBadRequest, "admin.stop_word_id_invalid", "The stop-word ID is invalid.")
		return
	}
	if err := h.store.DeleteStopWord(r.Context(), *id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "admin.stop_word_not_found", "The stop word was not found.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.stop_word_delete_failed", "The stop word could not be deleted.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listBlockedFeedArticles(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, http.StatusBadRequest, "admin.blocked_articles_limit_invalid", "The log limit must be between 1 and 200.")
			return
		}
		limit = parsed
	}
	items, err := h.store.ListBlockedFeedArticles(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.blocked_articles_load_failed", "The blocked-article log could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) allowBlockedFeedArticle(w http.ResponseWriter, r *http.Request) {
	h.moderateBlockedFeedArticle(w, r, true)
}

func (h *Handler) rejectBlockedFeedArticle(w http.ResponseWriter, r *http.Request) {
	h.moderateBlockedFeedArticle(w, r, false)
}

func (h *Handler) moderateBlockedFeedArticle(w http.ResponseWriter, r *http.Request, allow bool) {
	id, ok := optionalID(chi.URLParam(r, "id"))
	if !ok || id == nil {
		writeError(w, http.StatusBadRequest, "admin.blocked_article_id_invalid", "The blocked-article ID is invalid.")
		return
	}
	user, _ := currentUser(r)
	err := h.store.ModerateBlockedFeedArticle(r.Context(), *id, allow, user.ID)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrQuarantineUnavailable) {
		writeError(w, http.StatusNotFound, "admin.blocked_article_not_found", "The blocked article was not found or is unavailable for moderation.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin.blocked_article_moderate_failed", "The blocked article could not be moderated.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validRole(role string) bool { return role == "Administrator" || role == "Reader" }
