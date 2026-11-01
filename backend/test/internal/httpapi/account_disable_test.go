package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

type adminUserJSON struct {
	ID         int64   `json:"id"`
	Email      string  `json:"email"`
	Role       string  `json:"role"`
	Disabled   bool    `json:"disabled"`
	DisabledAt *string `json:"disabledAt"`
}

func createTestUser(t *testing.T, pool *pgxpool.Pool, email, password, role string) int64 {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (email,password_hash,role) VALUES ($1,$2,$3) RETURNING id`, email, hash, role).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func sessionCount(t *testing.T, pool *pgxpool.Pool, userID int64) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM sessions WHERE user_id=$1`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestDisablingAccountRevokesSessionsAndBlocksSignIn(t *testing.T) {
	pool, adminID, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	disabled := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(readerID), `{"disabled":true}`, adminCookie)
	var user adminUserJSON
	if disabled.Code != http.StatusOK || json.Unmarshal(disabled.Body.Bytes(), &user) != nil || !user.Disabled || user.DisabledAt == nil {
		t.Fatalf("disable reader = %d %s, want 200 with disabled user", disabled.Code, disabled.Body.String())
	}
	if count := sessionCount(t, pool, readerID); count != 0 {
		t.Fatalf("disabled reader keeps %d sessions, want 0", count)
	}
	if response := authenticatedRequest(t, handler, http.MethodGet, "/api/auth/me", "", readerCookie); response.Code != http.StatusUnauthorized {
		t.Fatalf("disabled reader's existing session = %d, want 401", response.Code)
	}
	login := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", `{"email":"reader@example.com","password":"a-long-reader-password"}`, nil)
	assertErrorCode(t, login, http.StatusForbidden, "auth.account_disabled")
	if len(login.Result().Cookies()) != 0 || sessionCount(t, pool, readerID) != 0 {
		t.Fatal("login of a disabled account must not create a session")
	}
	// A wrong password for a disabled account must not reveal that it is disabled.
	wrong := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", `{"email":"reader@example.com","password":"not-the-password"}`, nil)
	assertErrorCode(t, wrong, http.StatusUnauthorized, "auth.invalid_credentials")

	var disabledBy int64
	if err := pool.QueryRow(context.Background(), `SELECT disabled_by_user_id FROM users WHERE id=$1`, readerID).Scan(&disabledBy); err != nil || disabledBy != adminID {
		t.Fatalf("disabled_by_user_id = %d (%v), want %d", disabledBy, err, adminID)
	}

	list := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/users", "", adminCookie)
	var users []adminUserJSON
	if err := json.Unmarshal(list.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	for _, listed := range users {
		if listed.ID == readerID && !listed.Disabled {
			t.Fatalf("admin user list does not mark reader disabled: %s", list.Body.String())
		}
		if listed.ID == adminID && listed.Disabled {
			t.Fatalf("admin user list marks admin disabled: %s", list.Body.String())
		}
	}

	enabled := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(readerID), `{"disabled":false}`, adminCookie)
	user = adminUserJSON{}
	if enabled.Code != http.StatusOK || json.Unmarshal(enabled.Body.Bytes(), &user) != nil || user.Disabled || user.DisabledAt != nil {
		t.Fatalf("enable reader = %d %s", enabled.Code, enabled.Body.String())
	}
	loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	invalid := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(readerID), `{"disabled":"yes"}`, adminCookie)
	assertErrorCode(t, invalid, http.StatusBadRequest, "admin.disabled_invalid")
	unknown := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(readerID), `{"disabled":true,"deleted":true}`, adminCookie)
	assertErrorCode(t, unknown, http.StatusBadRequest, "admin.user_field_unknown")
}

func TestUserForSessionRejectsDisabledUser(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	if err := repository.CreateSession(ctx, readerID, "hash-of-a-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Defence in depth: even if a session row survived, a disabled account is rejected.
	if _, err := pool.Exec(ctx, `UPDATE users SET disabled_at=NOW() WHERE id=$1`, readerID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UserForSession(ctx, "hash-of-a-token"); err != store.ErrNotFound {
		t.Fatalf("UserForSession for disabled user = %v, want ErrNotFound", err)
	}
}

func TestAdministratorCannotDisableSelfOrLastActiveAdministrator(t *testing.T) {
	ctx := context.Background()
	pool, adminID, _ := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)

	self := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(adminID), `{"disabled":true}`, adminCookie)
	assertErrorCode(t, self, http.StatusConflict, "admin.cannot_disable_self")
	ownRole := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(adminID), `{"role":"Reader"}`, adminCookie)
	assertErrorCode(t, ownRole, http.StatusConflict, "admin.cannot_change_own_role")
	sameRole := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(adminID), `{"role":"Administrator","disabled":false}`, adminCookie)
	if sameRole.Code != http.StatusOK || sessionCount(t, pool, adminID) != 1 {
		t.Fatalf("no-op update of own account = %d %s, sessions %d", sameRole.Code, sameRole.Body.String(), sessionCount(t, pool, adminID))
	}

	// A disabled second administrator does not count as an active administrator.
	otherAdminID := createTestUser(t, pool, "second-admin@example.com", "a-long-second-password", "Administrator")
	if err := repository.UpdateUserAdmin(ctx, otherAdminID, store.UserAdminUpdate{Disabled: boolPointer(true)}, adminID); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateUserAdmin(ctx, adminID, store.UserAdminUpdate{Disabled: boolPointer(true)}, otherAdminID); err != store.ErrLastAdministrator {
		t.Fatalf("disable last active administrator = %v, want ErrLastAdministrator", err)
	}
	reader := "Reader"
	if err := repository.UpdateUserAdmin(ctx, adminID, store.UserAdminUpdate{Role: &reader}, otherAdminID); err != store.ErrLastAdministrator {
		t.Fatalf("demote last active administrator = %v, want ErrLastAdministrator", err)
	}
	// With a second active administrator, either may be disabled by the other.
	if err := repository.UpdateUserAdmin(ctx, otherAdminID, store.UserAdminUpdate{Disabled: boolPointer(false)}, adminID); err != nil {
		t.Fatal(err)
	}
	disableOther := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(otherAdminID), `{"disabled":true}`, adminCookie)
	if disableOther.Code != http.StatusOK {
		t.Fatalf("disable second administrator = %d %s", disableOther.Code, disableOther.Body.String())
	}
}

func TestRoleChangeRevokesTargetSessions(t *testing.T) {
	pool, adminID, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	interval := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(readerID), `{"refreshIntervalMinutes":30,"role":"Reader"}`, adminCookie)
	if interval.Code != http.StatusOK || sessionCount(t, pool, readerID) != 1 {
		t.Fatalf("unchanged role must keep sessions: %d %s, sessions %d", interval.Code, interval.Body.String(), sessionCount(t, pool, readerID))
	}
	promoted := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(readerID), `{"role":"Administrator"}`, adminCookie)
	if promoted.Code != http.StatusOK {
		t.Fatalf("promote reader = %d %s", promoted.Code, promoted.Body.String())
	}
	if response := authenticatedRequest(t, handler, http.MethodGet, "/api/auth/me", "", readerCookie); response.Code != http.StatusUnauthorized {
		t.Fatalf("session after role change = %d, want 401", response.Code)
	}
	if sessionCount(t, pool, adminID) != 1 {
		t.Fatal("changing another user's role must not revoke the caller's session")
	}
}

func TestAdministratorSignsOutUserEverywhere(t *testing.T) {
	pool, adminID, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	readerFirst := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)
	readerSecond := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	readerAttempt := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/users/"+intString(adminID)+"/sign-out", "", readerFirst)
	if readerAttempt.Code != http.StatusForbidden {
		t.Fatalf("reader sign-out of administrator = %d, want 403", readerAttempt.Code)
	}
	signOut := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/users/"+intString(readerID)+"/sign-out", "", adminCookie)
	if signOut.Code != http.StatusNoContent {
		t.Fatalf("sign out reader = %d %s, want 204", signOut.Code, signOut.Body.String())
	}
	for _, cookie := range []*http.Cookie{readerFirst, readerSecond} {
		if response := authenticatedRequest(t, handler, http.MethodGet, "/api/auth/me", "", cookie); response.Code != http.StatusUnauthorized {
			t.Fatalf("reader session after sign-out = %d, want 401", response.Code)
		}
	}
	if response := authenticatedRequest(t, handler, http.MethodGet, "/api/auth/me", "", adminCookie); response.Code != http.StatusOK {
		t.Fatalf("administrator session after signing out another user = %d, want 200", response.Code)
	}
	missing := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/users/999999/sign-out", "", adminCookie)
	assertErrorCode(t, missing, http.StatusNotFound, "user.not_found")

	self := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/users/"+intString(adminID)+"/sign-out", "", adminCookie)
	if self.Code != http.StatusNoContent {
		t.Fatalf("sign out self = %d, want 204", self.Code)
	}
	cleared := false
	for _, cookie := range self.Result().Cookies() {
		if cookie.Name == "pulse_session" && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("signing out yourself everywhere must clear the session cookie")
	}
	if response := authenticatedRequest(t, handler, http.MethodGet, "/api/auth/me", "", adminCookie); response.Code != http.StatusUnauthorized {
		t.Fatalf("own session after signing out everywhere = %d, want 401", response.Code)
	}
}

func boolPointer(value bool) *bool { return &value }
