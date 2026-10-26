package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/store"
)

const sessionCookieName = "pulse_session"

const sessionLifetime = 14 * 24 * time.Hour

type sessionUserKey struct{}

func (h *Handler) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if !isSameOrigin(r) {
				writeError(w, http.StatusForbidden, "auth.same_origin", "This request must come from this site.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isSameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		if referer, err := url.Parse(r.Header.Get("Referer")); err == nil && referer != nil {
			origin = referer.Scheme + "://" + referer.Host
		}
	}
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host)
}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "auth.sign_in_required", "Please sign in to continue.")
			return
		}
		user, err := h.store.UserForSession(r.Context(), auth.HashSessionToken(cookie.Value))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "auth.session_expired", "Your session has expired. Please sign in again.")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "auth.session_check_failed", "The session could not be verified.")
			return
		}
		ctx := context.WithValue(r.Context(), sessionUserKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) requireAdministrator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := currentUser(r)
		if !ok || user.Role != "Administrator" {
			writeError(w, http.StatusForbidden, "auth.administrator_required", "Administrator access is required.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func currentUser(r *http.Request) (store.User, bool) {
	user, ok := r.Context().Value(sessionUserKey{}).(store.User)
	return user, ok
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	if !validEmail(request.Email) || request.Password == "" {
		writeError(w, http.StatusBadRequest, "auth.invalid_login_request", "Enter a valid email address and password.")
		return
	}
	user, passwordHash, err := h.store.UserCredentialByEmail(r.Context(), request.Email)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !auth.VerifyPassword(passwordHash, request.Password)) {
		writeError(w, http.StatusUnauthorized, "auth.invalid_credentials", "The email address or password is incorrect.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth.account_check_failed", "The account could not be verified.")
		return
	}
	token, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth.session_create_failed", "A session could not be created.")
		return
	}
	expiresAt := time.Now().Add(sessionLifetime)
	if err := h.store.CreateSession(r.Context(), user.ID, auth.HashSessionToken(token), expiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "auth.session_create_failed", "A session could not be created.")
		return
	}
	h.setSessionCookie(w, token, expiresAt)
	writeJSON(w, http.StatusOK, user)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if err := h.store.RevokeSession(r.Context(), auth.HashSessionToken(cookie.Value)); err != nil {
			writeError(w, http.StatusInternalServerError, "auth.sign_out_failed", "You could not be signed out.")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	writeJSON(w, http.StatusOK, user)
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	var request struct {
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		Nickname  string `json:"nickname"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.FirstName = strings.TrimSpace(request.FirstName)
	request.LastName = strings.TrimSpace(request.LastName)
	request.Nickname = strings.TrimSpace(request.Nickname)
	if utf8.RuneCountInString(request.FirstName) > 80 || utf8.RuneCountInString(request.LastName) > 80 || utf8.RuneCountInString(request.Nickname) > 40 {
		writeError(w, http.StatusBadRequest, "profile.fields_too_long", "First and last names must be 80 characters or fewer; nicknames must be 40 characters or fewer.")
		return
	}
	user, _ := currentUser(r)
	updated, err := h.store.UpdateUserProfile(r.Context(), user.ID, request.FirstName, request.LastName, request.Nickname)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user.not_found", "The user was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile.save_failed", "The profile could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len([]rune(request.NewPassword)) < 12 {
		writeError(w, http.StatusBadRequest, "auth.password_too_short", "The new password must contain at least 12 characters.")
		return
	}
	user, _ := currentUser(r)
	_, passwordHash, err := h.store.UserCredentialByEmail(r.Context(), user.Email)
	if err != nil || !auth.VerifyPassword(passwordHash, request.CurrentPassword) {
		writeError(w, http.StatusForbidden, "auth.current_password_incorrect", "The current password is incorrect.")
		return
	}
	newHash, err := auth.HashPassword(request.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth.password_change_failed", "The password could not be changed.")
		return
	}
	cookie, _ := r.Cookie(sessionCookieName)
	if err := h.store.ChangeUserPassword(r.Context(), user.ID, newHash, auth.HashSessionToken(cookie.Value)); err != nil {
		writeError(w, http.StatusInternalServerError, "auth.password_change_failed", "The password could not be changed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: h.secureCookie, SameSite: http.SameSiteStrictMode,
		Expires: expiresAt, MaxAge: int(sessionLifetime.Seconds()),
	})
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

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

func validRole(role string) bool { return role == "Administrator" || role == "Reader" }
