package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

func TestAuthenticationRolesAndSameOriginProtection(t *testing.T) {
	ctx := context.Background()
	pool, adminID, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), true)

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/folders", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /api/folders = %d, want 401", unauthenticated.Code)
	}
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("public health = %d, want 200", health.Code)
	}

	csrf := httptest.NewRecorder()
	handler.ServeHTTP(csrf, httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"a-long-admin-password"}`)))
	if csrf.Code != http.StatusForbidden {
		t.Fatalf("login without same-origin header = %d, want 403", csrf.Code)
	}

	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", true)
	if !adminCookie.HttpOnly || adminCookie.SameSite != http.SameSiteStrictMode || !adminCookie.Secure {
		t.Fatalf("session cookie flags = HttpOnly:%v SameSite:%v Secure:%v", adminCookie.HttpOnly, adminCookie.SameSite, adminCookie.Secure)
	}
	if !adminCookie.Expires.After(time.Now().AddDate(0, 0, 13)) || !adminCookie.Expires.Before(time.Now().AddDate(0, 0, 15)) {
		t.Fatalf("session cookie expiry = %v, want approximately 14 days", adminCookie.Expires)
	}
	adminUsers := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/users", "", adminCookie)
	if adminUsers.Code != http.StatusOK {
		t.Fatalf("Administrator GET /api/admin/users = %d, want 200", adminUsers.Code)
	}
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", true)
	readerUsers := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/users", "", readerCookie)
	if readerUsers.Code != http.StatusForbidden {
		t.Fatalf("Reader GET /api/admin/users = %d, want 403", readerUsers.Code)
	}
	readerSelf := authenticatedRequest(t, handler, http.MethodGet, "/api/auth/me", "", readerCookie)
	var self struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if readerSelf.Code != http.StatusOK || json.Unmarshal(readerSelf.Body.Bytes(), &self) != nil || self.ID != readerID || self.Role != "Reader" {
		t.Fatalf("Reader GET /api/auth/me = %d %s", readerSelf.Code, readerSelf.Body.String())
	}
	if adminID == readerID {
		t.Fatal("test users must have distinct IDs")
	}
	readerLibrary := authenticatedRequest(t, handler, http.MethodGet, "/api/articles?filter=all", "", readerCookie)
	var readerPage struct {
		Items []json.RawMessage `json:"items"`
	}
	if readerLibrary.Code != http.StatusOK || json.Unmarshal(readerLibrary.Body.Bytes(), &readerPage) != nil || len(readerPage.Items) != 0 {
		t.Fatalf("Reader library leaked another account's article: %d %s", readerLibrary.Code, readerLibrary.Body.String())
	}
	var adminArticleID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM articles LIMIT 1`).Scan(&adminArticleID); err != nil {
		t.Fatal(err)
	}
	crossUserUpdate := authenticatedRequest(t, handler, http.MethodPatch, "/api/articles/"+intString(adminArticleID), `{"isRead":true}`, readerCookie)
	if crossUserUpdate.Code != http.StatusNotFound {
		t.Fatalf("Reader PATCH of another account's article = %d, want 404", crossUserUpdate.Code)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`); err != nil {
		t.Fatal(err)
	}
	noOrigin := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(`{"email":"new@example.com","password":"a-long-new-password","role":"Reader"}`))
	noOrigin.AddCookie(adminCookie)
	noOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(noOriginResponse, noOrigin)
	if noOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("mutating admin request without same-origin header = %d, want 403", noOriginResponse.Code)
	}
}

func TestAdministratorCanCreateUsersAndCannotDemoteLastAdministrator(t *testing.T) {
	pool, adminID, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	created := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/users", `{"email":"new@example.com","password":"a-long-new-password","role":"Reader"}`, adminCookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create Reader = %d %s, want 201", created.Code, created.Body.String())
	}
	duplicate := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/users", `{"email":"NEW@example.com","password":"a-long-new-password","role":"Reader"}`, adminCookie)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate email = %d, want 409", duplicate.Code)
	}
	invalidRole := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/users", `{"email":"other@example.com","password":"a-long-new-password","role":"Owner"}`, adminCookie)
	if invalidRole.Code != http.StatusBadRequest {
		t.Fatalf("invalid role = %d, want 400", invalidRole.Code)
	}
	demote := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(adminID), `{"role":"Reader"}`, adminCookie)
	if demote.Code != http.StatusConflict {
		t.Fatalf("last Administrator demotion = %d, want 409", demote.Code)
	}
}

func TestReaderCanRenameAndRecolorOnlyTheirOwnFolders(t *testing.T) {
	ctx := context.Background()
	pool, adminID, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	created := authenticatedRequest(t, handler, http.MethodPost, "/api/folders", `{"name":"Reading","color":"amber"}`, readerCookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create folder = %d %s, want 201", created.Code, created.Body.String())
	}
	var folder store.Folder
	if err := json.Unmarshal(created.Body.Bytes(), &folder); err != nil || folder.Color != "amber" {
		t.Fatalf("created folder = %#v, unmarshal error %v", folder, err)
	}

	updated := authenticatedRequest(t, handler, http.MethodPatch, "/api/folders/"+intString(folder.ID), `{"name":"News desk","color":"blue"}`, readerCookie)
	if updated.Code != http.StatusOK || json.Unmarshal(updated.Body.Bytes(), &folder) != nil || folder.Name != "News desk" || folder.Color != "blue" {
		t.Fatalf("update folder = %d %s, want renamed blue folder", updated.Code, updated.Body.String())
	}
	foldersResponse := authenticatedRequest(t, handler, http.MethodGet, "/api/folders", "", readerCookie)
	var folders []store.Folder
	if foldersResponse.Code != http.StatusOK || json.Unmarshal(foldersResponse.Body.Bytes(), &folders) != nil {
		t.Fatalf("list updated folders = %d %s", foldersResponse.Code, foldersResponse.Body.String())
	}
	if len(folders) != 1 || folders[0].ID != folder.ID || folders[0].Color != "blue" || folders[0].Name != "News desk" {
		t.Fatalf("listed folders after update = %#v, want persisted renamed blue folder", folders)
	}
	customColor := authenticatedRequest(t, handler, http.MethodPatch, "/api/folders/"+intString(folder.ID), `{"name":"News desk","color":"#12ABEF"}`, readerCookie)
	if customColor.Code != http.StatusOK || json.Unmarshal(customColor.Body.Bytes(), &folder) != nil || folder.Color != "#12abef" {
		t.Fatalf("custom folder color = %d %s, want normalized #12abef", customColor.Code, customColor.Body.String())
	}
	foldersResponse = authenticatedRequest(t, handler, http.MethodGet, "/api/folders", "", readerCookie)
	if foldersResponse.Code != http.StatusOK || json.Unmarshal(foldersResponse.Body.Bytes(), &folders) != nil || len(folders) != 1 || folders[0].Color != "#12abef" {
		t.Fatalf("listed custom folder color = %d %s, want persisted #12abef", foldersResponse.Code, foldersResponse.Body.String())
	}

	adminFolder, err := store.New(pool).CreateFolderForUser(ctx, adminID, "Private folder", store.DefaultFolderColor)
	if err != nil {
		t.Fatal(err)
	}
	foreignUpdate := authenticatedRequest(t, handler, http.MethodPatch, "/api/folders/"+intString(adminFolder.ID), `{"name":"Stolen","color":"rose"}`, readerCookie)
	if foreignUpdate.Code != http.StatusNotFound {
		t.Fatalf("edit another user's folder = %d, want 404", foreignUpdate.Code)
	}
	invalidColor := authenticatedRequest(t, handler, http.MethodPatch, "/api/folders/"+intString(folder.ID), `{"name":"News desk","color":"#fff"}`, readerCookie)
	if invalidColor.Code != http.StatusBadRequest {
		t.Fatalf("invalid folder color = %d, want 400", invalidColor.Code)
	}
}

func TestReaderCanDeleteFolderAndRemoveOrMoveOnlyTheirSubscriptions(t *testing.T) {
	ctx := context.Background()
	pool, adminID, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)

	removeFolder, err := store.New(pool).CreateFolderForUser(ctx, adminID, "Remove sources", store.DefaultFolderColor)
	if err != nil {
		t.Fatal(err)
	}
	var sharedSourceID, articleID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://shared-delete.example/rss','Shared','https://shared-delete.example/rss') RETURNING id`).Scan(&sharedSourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO articles (feed_source_id,guid,title) VALUES ($1,'item','Shared article') RETURNING id`, sharedSourceID).Scan(&articleID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id,folder_id) VALUES ($1,$2,$3),($4,$2,NULL)`, adminID, sharedSourceID, removeFolder.ID, readerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_article_states (user_id,article_id,is_saved) VALUES ($1,$2,TRUE),($3,$2,TRUE)`, adminID, articleID, readerID); err != nil {
		t.Fatal(err)
	}
	removed := authenticatedRequest(t, handler, http.MethodDelete, "/api/folders/"+intString(removeFolder.ID), `{"action":"delete_sources"}`, adminCookie)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("delete folder and subscriptions = %d %s, want 204", removed.Code, removed.Body.String())
	}
	var adminSubscriptions, readerSubscriptions, sourceArticles, adminStates, readerStates int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_feeds WHERE feed_source_id=$1 AND user_id=$2`, sharedSourceID, adminID).Scan(&adminSubscriptions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_feeds WHERE feed_source_id=$1 AND user_id=$2`, sharedSourceID, readerID).Scan(&readerSubscriptions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM articles WHERE id=$1`, articleID).Scan(&sourceArticles); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_article_states WHERE article_id=$1 AND user_id=$2`, articleID, adminID).Scan(&adminStates); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_article_states WHERE article_id=$1 AND user_id=$2`, articleID, readerID).Scan(&readerStates); err != nil {
		t.Fatal(err)
	}
	if adminSubscriptions != 0 || readerSubscriptions != 1 || sourceArticles != 1 || adminStates != 0 || readerStates != 1 {
		t.Fatalf("after removing folder subscriptions admin=%d reader=%d articles=%d admin states=%d reader states=%d", adminSubscriptions, readerSubscriptions, sourceArticles, adminStates, readerStates)
	}

	moveFolder, err := store.New(pool).CreateFolderForUser(ctx, adminID, "Move from", store.DefaultFolderColor)
	if err != nil {
		t.Fatal(err)
	}
	targetFolder, err := store.New(pool).CreateFolderForUser(ctx, adminID, "Move to", store.DefaultFolderColor)
	if err != nil {
		t.Fatal(err)
	}
	var movedSourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://move-delete.example/rss','Move','https://move-delete.example/rss') RETURNING id`).Scan(&movedSourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id,folder_id) VALUES ($1,$2,$3)`, adminID, movedSourceID, moveFolder.ID); err != nil {
		t.Fatal(err)
	}
	moved := authenticatedRequest(t, handler, http.MethodDelete, "/api/folders/"+intString(moveFolder.ID), `{"action":"move_sources","targetFolderId":`+intString(targetFolder.ID)+`}`, adminCookie)
	if moved.Code != http.StatusNoContent {
		t.Fatalf("move subscriptions while deleting folder = %d %s, want 204", moved.Code, moved.Body.String())
	}
	var actualFolderID int64
	if err := pool.QueryRow(ctx, `SELECT folder_id FROM user_feeds WHERE user_id=$1 AND feed_source_id=$2`, adminID, movedSourceID).Scan(&actualFolderID); err != nil || actualFolderID != targetFolder.ID {
		t.Fatalf("moved source folder = %d, error %v, want %d", actualFolderID, err, targetFolder.ID)
	}
	invalidTarget := authenticatedRequest(t, handler, http.MethodDelete, "/api/folders/"+intString(targetFolder.ID), `{"action":"move_sources","targetFolderId":`+intString(targetFolder.ID)+`}`, adminCookie)
	if invalidTarget.Code != http.StatusBadRequest {
		t.Fatalf("move folder into itself = %d, want 400", invalidTarget.Code)
	}
}

func TestAdminIntervalSettingsPersistAndPasswordChangeKeepsOnlyCurrentSession(t *testing.T) {
	pool, _, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	firstSession := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	otherSession := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	updatedSettings := authenticatedRequest(t, handler, http.MethodPut, "/api/admin/settings", `{"refreshIntervalMinutes":30,"sharedFeedTTLMinutes":45}`, firstSession)
	if updatedSettings.Code != http.StatusOK {
		t.Fatalf("PUT /api/admin/settings = %d %s", updatedSettings.Code, updatedSettings.Body.String())
	}
	invalidSettings := authenticatedRequest(t, handler, http.MethodPut, "/api/admin/settings", `{"refreshIntervalMinutes":4,"sharedFeedTTLMinutes":45}`, firstSession)
	if invalidSettings.Code != http.StatusBadRequest {
		t.Fatalf("interval below 5 minutes = %d, want 400", invalidSettings.Code)
	}
	interval := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(readerID), `{"refreshIntervalMinutes":60}`, firstSession)
	if interval.Code != http.StatusOK {
		t.Fatalf("set per-user interval = %d %s", interval.Code, interval.Body.String())
	}
	inherit := authenticatedRequest(t, handler, http.MethodPatch, "/api/admin/users/"+intString(readerID), `{"refreshIntervalMinutes":null}`, firstSession)
	if inherit.Code != http.StatusOK || !strings.Contains(inherit.Body.String(), `"refreshIntervalMinutes":null`) {
		t.Fatalf("restore global interval inheritance = %d %s", inherit.Code, inherit.Body.String())
	}
	changedPassword := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/password", `{"currentPassword":"a-long-admin-password","newPassword":"a-new-long-admin-password"}`, firstSession)
	if changedPassword.Code != http.StatusNoContent {
		t.Fatalf("change password = %d %s", changedPassword.Code, changedPassword.Body.String())
	}
	if current := authenticatedRequest(t, handler, http.MethodGet, "/api/auth/me", "", firstSession); current.Code != http.StatusOK {
		t.Fatalf("current session after password change = %d, want 200", current.Code)
	}
	if revoked := authenticatedRequest(t, handler, http.MethodGet, "/api/auth/me", "", otherSession); revoked.Code != http.StatusUnauthorized {
		t.Fatalf("other session after password change = %d, want 401", revoked.Code)
	}
}

func TestManualRefreshReturnsConflictWhenRefreshSlotIsBusy(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	control := syncer.NewControl()
	if !control.Request(true) {
		t.Fatal("failed to occupy refresh controller for the test")
	}
	handler := httpapi.New(store.New(pool), control, false)
	cookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	response := authenticatedRequest(t, handler, http.MethodPost, "/api/refresh", "", cookie)
	if response.Code != http.StatusConflict {
		t.Fatalf("manual refresh while controller is occupied = %d %s, want 409", response.Code, response.Body.String())
	}
}

func TestNewSubscriberReusesFreshSharedSourceWithoutFetchingAgain(t *testing.T) {
	pool, adminID, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	adminFolderID := int64(0)
	if err := pool.QueryRow(context.Background(), `SELECT id FROM folders WHERE owner_user_id=$1 ORDER BY id LIMIT 1`, adminID).Scan(&adminFolderID); err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO feed_sources (normalized_url,title,url,last_success_at) VALUES ('https://fresh.example/rss','Fresh feed','https://fresh.example/rss',NOW()) RETURNING id`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO user_feeds (user_id,feed_source_id,folder_id) VALUES ($1,$2,$3)`, adminID, sourceID, adminFolderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO articles (feed_source_id,guid,title) VALUES ($1,'fresh-item','Shared item')`, sourceID); err != nil {
		t.Fatal(err)
	}
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)
	response := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"HTTPS://Fresh.example:443/rss#fragment","folderId":null}`, readerCookie)
	if response.Code != http.StatusCreated {
		t.Fatalf("subscribe to fresh canonical source = %d %s, want 201", response.Code, response.Body.String())
	}
	var sources, subscriptions int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM feed_sources WHERE normalized_url='https://fresh.example/rss'`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM user_feeds WHERE feed_source_id=$1`, sourceID).Scan(&subscriptions); err != nil {
		t.Fatal(err)
	}
	if sources != 1 || subscriptions != 2 {
		t.Fatalf("canonical sources=%d subscriptions=%d, want one source and two users", sources, subscriptions)
	}
	articles := authenticatedRequest(t, handler, http.MethodGet, "/api/articles?filter=all", "", readerCookie)
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	if articles.Code != http.StatusOK || json.Unmarshal(articles.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
		t.Fatalf("new subscriber's shared article view = %d %s, want one article", articles.Code, articles.Body.String())
	}
}

func isolatedAPIDatabase(t *testing.T) (*pgxpool.Pool, int64, int64) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "api_test_" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_") + "_" + time.Now().UTC().Format("150405000")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	adminID, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "a-long-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, adminID); err != nil {
		t.Fatal(err)
	}
	readerHash, err := auth.HashPassword("a-long-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	var readerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email,password_hash,role) VALUES ('reader@example.com',$1,'Reader') RETURNING id`, readerHash).Scan(&readerID); err != nil {
		t.Fatal(err)
	}
	var adminFolderID, sourceID, articleID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM folders WHERE owner_user_id=$1 ORDER BY id LIMIT 1`, adminID).Scan(&adminFolderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url,last_success_at) VALUES ('https://example.com/rss','Admin feed','https://example.com/rss',NOW()) RETURNING id`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id,folder_id) VALUES ($1,$2,$3)`, adminID, sourceID, adminFolderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO articles (feed_source_id,guid,title) VALUES ($1,'only-admin','Private article') RETURNING id`, sourceID).Scan(&articleID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_article_states (user_id,article_id,is_read,is_saved) VALUES ($1,$2,FALSE,TRUE)`, adminID, articleID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	})
	return pool, adminID, readerID
}

func loginForTest(t *testing.T, handler http.Handler, email, password string, secure bool) *http.Cookie {
	t.Helper()
	response := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", `{"email":"`+email+`","password":"`+password+`"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("login %s = %d %s", email, response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	if cookie.Name != "pulse_session" {
		t.Fatalf("cookie name = %q", cookie.Name)
	}
	if cookie.Secure != secure {
		t.Fatalf("cookie Secure = %v, want %v", cookie.Secure, secure)
	}
	return cookie
}

func authenticatedRequest(t *testing.T, handler http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host = "example.test"
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("Origin", "http://example.test")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func intString(value int64) string {
	return strconv.FormatInt(value, 10)
}
