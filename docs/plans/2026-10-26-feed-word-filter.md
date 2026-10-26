# Feed Word Filter Implementation Plan

> **For Claude:** Use the Executing Plans skill to implement this plan task-by-task.

**Goal:** Let administrators manage a multilingual feed stop-word dictionary, filter matching articles before persistence, and review blocked articles with their feed submitter.

**Architecture:** Add persisted stop words and a blocked-item audit log in a numbered migration. Match normalized whole words and phrases in article titles and summaries inside the source refresh transaction, before article upserts. Expose administrator-only endpoints and add management and log tables to the existing administration screen.

**Tech Stack:** Go, PostgreSQL, Chi, React, TypeScript, i18next, Vitest.

---

### Task 1: Stop-word matching and refresh persistence

**Files:** `backend/internal/feeds/stopwords.go`, `backend/test/internal/feeds/stopwords_test.go`, `backend/internal/store/source_refresh.go`, `backend/internal/store/migrations/008_feed_word_filter.sql`, `backend/test/internal/store/feed_filter_test.go`

Add normalization and whole-token phrase matching, seed Russian/English spam, fraud, erotic, and pornographic terms, attribute each new shared source to its first submitter, log matched items, and exclude them from article upserts. Verify that clean feed items persist while matching items appear only in the block log.

### Task 2: Administrator API

**Files:** `backend/internal/store/feed_filter.go`, `backend/internal/httpapi/admin_handlers.go`, `backend/internal/httpapi/handler.go`, `backend/test/internal/httpapi/auth_test.go`.

Add list/add/delete stop-word endpoints and a recent blocked-item listing endpoint with a bounded limit behind administrator authorization. Validate and normalize submitted terms, reject empty or overlong entries, and map duplicate terms to conflict.

### Task 3: Administration UI and localization

**Files:** `frontend/src/api.ts`, `frontend/src/AdminPanel.tsx`, `frontend/src/locales/{en,de,ru}.json`, `frontend/test/src/AdminPanel.test.tsx`.

Add a compact dictionary editor and blocked-news table showing article, feed, submitter, matched term, and time. Keep loading, empty, add, and remove states accessible and localized.

### Task 4: Documentation and verification

Update `docs/PROJECT.md` and the operator guide with filtering behavior. Run focused Go and frontend tests, full relevant suites, frontend build, and `git diff --check`.
