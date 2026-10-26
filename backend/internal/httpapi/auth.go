package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

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
