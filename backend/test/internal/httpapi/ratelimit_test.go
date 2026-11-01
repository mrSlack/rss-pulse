package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func TestLoginIsRateLimitedPerAccountUntilWindowPasses(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	clock := &testClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false, httpapi.WithClock(clock.Now))
	const wrong = `{"email":"reader@example.com","password":"not-the-password"}`
	const correct = `{"email":"Reader@Example.com ","password":"a-long-reader-password"}`

	for attempt := 1; attempt <= 10; attempt++ {
		response := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", wrong, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d = %d %s, want 401", attempt, response.Code, response.Body.String())
		}
		clock.Advance(10 * time.Second)
	}
	limited := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", wrong, nil)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("11th failed attempt = %d %s, want 429", limited.Code, limited.Body.String())
	}
	var body struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(limited.Body.Bytes(), &body); err != nil || body.Code != "auth.rate_limited" || body.Error == "" {
		t.Fatalf("429 body = %s", limited.Body.String())
	}
	retryAfter, err := strconv.Atoi(limited.Header().Get("Retry-After"))
	// The first failure was 100 seconds ago; it leaves the 15-minute window in 800 seconds.
	if err != nil || retryAfter != 800 {
		t.Fatalf("Retry-After = %q, want 800", limited.Header().Get("Retry-After"))
	}

	lockedCorrect := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", correct, nil)
	if lockedCorrect.Code != http.StatusTooManyRequests || len(lockedCorrect.Result().Cookies()) != 0 {
		t.Fatalf("correct password while locked = %d, want 429 without a session", lockedCorrect.Code)
	}

	clock.Advance(time.Duration(retryAfter) * time.Second)
	loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)
	// Success cleared the remaining failures, so a new mistake is not locked out.
	if again := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", wrong, nil); again.Code != http.StatusUnauthorized {
		t.Fatalf("failed attempt after successful login = %d, want 401", again.Code)
	}
}

func TestUnknownAccountsAreRateLimitedLikeExistingOnes(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	clock := &testClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false, httpapi.WithClock(clock.Now))
	const unknown = `{"email":"nobody@example.com","password":"not-the-password"}`
	for attempt := 1; attempt <= 10; attempt++ {
		if response := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", unknown, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", attempt, response.Code)
		}
	}
	if response := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", unknown, nil); response.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt for unknown account = %d, want 429", response.Code)
	}
}

func loginFrom(t *testing.T, handler http.Handler, remote, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.RemoteAddr = remote
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// Two handlers on separate connection pools stand for two API instances (or
// one API before and after a restart) sharing the database.
func TestLoginRateLimitIsSharedBetweenInstances(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	secondPool, err := pgxpool.NewWithConfig(context.Background(), pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secondPool.Close)
	instances := []http.Handler{
		httpapi.New(store.New(pool), syncer.NewControl(), false),
		httpapi.New(store.New(secondPool), syncer.NewControl(), false),
	}
	const wrong = `{"email":"reader@example.com","password":"not-the-password"}`

	const concurrent = 16
	codes := make([]int, concurrent)
	var group sync.WaitGroup
	for index := range concurrent {
		group.Add(1)
		go func() {
			defer group.Done()
			codes[index] = loginFrom(t, instances[index%2], fmt.Sprintf("192.0.2.%d:4000", index), wrong).Code
		}()
	}
	group.Wait()
	counts := map[int]int{}
	for _, code := range codes {
		counts[code]++
	}
	if counts[http.StatusUnauthorized] != 10 || counts[http.StatusTooManyRequests] != concurrent-10 {
		t.Fatalf("status counts = %v, want 10 x 401 and %d x 429", counts, concurrent-10)
	}

	// A freshly started instance sees the lock as well.
	restarted := httpapi.New(store.New(pool), syncer.NewControl(), false)
	correct := `{"email":"reader@example.com","password":"a-long-reader-password"}`
	if response := loginFrom(t, restarted, "198.51.100.1:4000", correct); response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("correct password on a new instance while locked = %d, want 429 with Retry-After", response.Code)
	}
}

func TestLoginFailsClosedWhenRateLimitStateIsUnavailable(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	if _, err := pool.Exec(context.Background(), `DROP TABLE login_attempts`); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"not-the-password", "a-long-reader-password"} {
		response := loginFrom(t, handler, "192.0.2.1:4000", `{"email":"reader@example.com","password":"`+password+`"}`)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		if response.Code != http.StatusInternalServerError || body.Code != "auth.account_check_failed" || len(response.Result().Cookies()) != 0 {
			t.Fatalf("login with %q while limiter state is unavailable = %d %s, want 500 auth.account_check_failed without a session", password, response.Code, response.Body.String())
		}
	}
}
