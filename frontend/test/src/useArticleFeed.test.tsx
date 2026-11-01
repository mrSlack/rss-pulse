import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import '../../src/i18n';
import type { Article, ArticlePage } from '../../src/api';
import { useArticleFeed, type ArticleView } from '../../src/hooks/useArticleFeed';

const apiMock = vi.hoisted(() => ({
  getArticles: vi.fn(),
  updateArticle: vi.fn(),
  markArticlesRead: vi.fn(),
}));

vi.mock('../../src/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../src/api')>()),
  api: apiMock,
}));

function article(id: number, publishedAt: string): Article {
  return {
    id,
    feedId: 1,
    feedTitle: 'Example feed',
    feedUrl: 'https://example.com/feed.xml',
    title: `Article ${id}`,
    url: `https://example.com/articles/${id}`,
    summary: '',
    publishedAt,
    isRead: false,
    isSaved: false,
  };
}

function page(items: Article[], nextCursor: string | null, latestArticleId?: number): ArticlePage {
  return { items, hasMore: nextCursor !== null, nextCursor, latestArticleId };
}

const options = { onError: () => {}, onCountsChanged: () => Promise.resolve() };

function renderFeed(initial: ArticleView) {
  return renderHook((view: ArticleView) => useArticleFeed(view, options), { initialProps: initial });
}

const allView: ArticleView = { filter: 'all', feedId: null, folderId: null, search: '' };

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.markArticlesRead.mockResolvedValue({ marked: 1 });
});

afterEach(() => {
  cleanup();
});

describe('useArticleFeed loading more', () => {
  it('does not page the previous view after the view changed', async () => {
    let resolveSecond: (value: ArticlePage) => void = () => {};
    const oldFirst = page([article(1, '2026-10-31T12:00:00Z')], 'old-cursor');
    const newFirst = page([article(3, '2026-10-31T11:00:00Z')], null);
    apiMock.getArticles.mockImplementation((params: URLSearchParams) => {
      if (params.get('before') === 'old-cursor') return new Promise<ArticlePage>((resolve) => { resolveSecond = resolve; });
      return Promise.resolve(params.get('q') === 'news' ? newFirst : oldFirst);
    });
    const { result, rerender } = renderFeed(allView);
    await waitFor(() => expect(result.current.articles.map((item) => item.id)).toEqual([1]));

    // The search is debounced, so the old view's cursor is still loaded here
    // (the scroll sentinel or `j` near the end can trigger this).
    rerender({ ...allView, search: 'news' });
    let pending: Promise<void> = Promise.resolve();
    act(() => { pending = result.current.loadMore(); });
    await waitFor(() => expect(result.current.articles.map((item) => item.id)).toEqual([3]));
    await act(async () => {
      resolveSecond(page([article(2, '2026-10-30T12:00:00Z')], 'old-cursor-2'));
      await pending;
    });

    expect(result.current.articles.map((item) => item.id)).toEqual([3]);
    expect(result.current.hasMore).toBe(false);
    expect(apiMock.getArticles.mock.calls.some(([params]) => (params as URLSearchParams).get('before') === 'old-cursor')).toBe(false);
  });

  it('discards a page that resolves after the view changed', async () => {
    let resolveSecond: (value: ArticlePage) => void = () => {};
    const oldFirst = page([article(1, '2026-10-31T12:00:00Z')], 'old-cursor');
    const newFirst = page([article(3, '2026-10-31T11:00:00Z')], null);
    apiMock.getArticles.mockImplementation((params: URLSearchParams) => {
      if (params.get('before') === 'old-cursor') return new Promise<ArticlePage>((resolve) => { resolveSecond = resolve; });
      return Promise.resolve(params.get('q') === 'news' ? newFirst : oldFirst);
    });
    const { result, rerender } = renderFeed(allView);
    await waitFor(() => expect(result.current.articles.map((item) => item.id)).toEqual([1]));

    let pending: Promise<void> = Promise.resolve();
    act(() => { pending = result.current.loadMore(); });
    rerender({ ...allView, search: 'news' });
    await waitFor(() => expect(result.current.articles.map((item) => item.id)).toEqual([3]));
    await act(async () => {
      resolveSecond(page([article(2, '2026-10-30T12:00:00Z')], 'old-cursor-2'));
      await pending;
    });

    expect(result.current.articles.map((item) => item.id)).toEqual([3]);
    expect(result.current.hasMore).toBe(false);
  });
});

describe('useArticleFeed mark all as read', () => {
  it('bounds the request by the ingestion position of the loaded list', async () => {
    apiMock.getArticles.mockResolvedValue(page([article(5, '2026-10-31T12:00:00Z'), article(4, '2026-10-31T11:00:00Z')], 'next', 42));
    const { result } = renderFeed(allView);
    await waitFor(() => expect(result.current.articles).toHaveLength(2));
    // A page loaded later may contain an article ingested after the first page.
    apiMock.getArticles.mockResolvedValue(page([article(43, '2026-10-01T00:00:00Z'), article(7, '2026-09-30T00:00:00Z')], null, 43));
    await act(async () => { await result.current.loadMore(); });
    expect(result.current.articles).toHaveLength(4);

    await act(async () => { await result.current.markAllRead(); });

    expect(apiMock.markArticlesRead).toHaveBeenCalledWith(
      { filter: 'all', feedId: null, folderId: null, q: '', since: null },
      { upToId: 42 },
    );
    expect(result.current.articles.filter((item) => item.isRead).map((item) => item.id)).toEqual([5, 4, 7]);
  });

  it('falls back to the newest publication date for servers without an ingestion bound', async () => {
    apiMock.getArticles.mockResolvedValue(page([article(5, '2026-10-31T12:00:00.123456Z'), article(4, '2026-10-31T11:00:00Z')], null));
    const { result } = renderFeed(allView);
    await waitFor(() => expect(result.current.articles).toHaveLength(2));

    await act(async () => { await result.current.markAllRead(); });

    expect(apiMock.markArticlesRead).toHaveBeenCalledWith(
      { filter: 'all', feedId: null, folderId: null, q: '', since: null },
      { upTo: '2026-10-31T12:00:00.123456Z' },
    );
    expect(result.current.articles.every((item) => item.isRead)).toBe(true);
  });
});
