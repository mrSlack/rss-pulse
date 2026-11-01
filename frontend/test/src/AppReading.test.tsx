import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../../src/App';
import type { Article } from '../../src/api';

const apiMock = vi.hoisted(() => ({
  currentUser: vi.fn(),
  getFeeds: vi.fn(),
  getFolders: vi.fn(),
  getArticles: vi.fn(),
  updateArticle: vi.fn(),
}));

vi.mock('../../src/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../src/api')>()),
  api: apiMock,
}));

type ObserverCallback = (entries: Array<{ isIntersecting: boolean }>) => void;
let observerCallbacks: ObserverCallback[] = [];

class FakeIntersectionObserver {
  private callback: ObserverCallback;
  constructor(callback: ObserverCallback) {
    this.callback = callback;
    observerCallbacks.push(callback);
  }
  observe() {}
  disconnect() {
    observerCallbacks = observerCallbacks.filter((callback) => callback !== this.callback);
  }
}

function article(id: number, overrides: Partial<Article> = {}): Article {
  return {
    id,
    feedId: 1,
    feedTitle: 'Example feed',
    feedUrl: 'https://example.com/feed.xml',
    title: `Article ${id}`,
    url: '',
    summary: `Summary ${id}`,
    publishedAt: '2026-10-31T12:00:00Z',
    isRead: false,
    isSaved: false,
    ...overrides,
  };
}

function articleCards() {
  return document.querySelectorAll('.article-card');
}

beforeEach(() => {
  vi.clearAllMocks();
  observerCallbacks = [];
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
  apiMock.currentUser.mockResolvedValue({ id: 3, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null });
  apiMock.getFeeds.mockResolvedValue([]);
  apiMock.getFolders.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('article paging', () => {
  it('requests the next page with the server cursor and skips duplicate articles', async () => {
    const firstPage = Array.from({ length: 20 }, (_, index) => article(index + 1));
    apiMock.getArticles
      .mockResolvedValueOnce({ items: firstPage, hasMore: true, nextCursor: 'cursor-after-20' })
      .mockResolvedValueOnce({ items: [article(20), article(21)], hasMore: false, nextCursor: null });
    render(<App />);

    await waitFor(() => expect(articleCards()).toHaveLength(20));
    await waitFor(() => expect(observerCallbacks.length).toBeGreaterThan(0));
    await act(async () => observerCallbacks.at(-1)!([{ isIntersecting: true }]));

    await waitFor(() => expect(articleCards()).toHaveLength(21));
    expect(apiMock.getArticles).toHaveBeenCalledTimes(2);
    const params = apiMock.getArticles.mock.calls[1][0] as URLSearchParams;
    expect(params.get('before')).toBe('cursor-after-20');
    expect(params.has('offset')).toBe(false);
    expect(params.get('limit')).toBe('20');
  });
});

describe('read and saved toggles', () => {
  it('updates the article locally and refreshes only counters when toggling read', async () => {
    const user = userEvent.setup();
    apiMock.getArticles.mockResolvedValue({ items: [article(1), article(2)], hasMore: false, nextCursor: null });
    apiMock.updateArticle.mockResolvedValue(article(1, { isRead: true }));
    render(<App />);

    await waitFor(() => expect(articleCards()).toHaveLength(2));
    const feedsCalls = apiMock.getFeeds.mock.calls.length;
    const foldersCalls = apiMock.getFolders.mock.calls.length;
    await user.click(within(articleCards()[0] as HTMLElement).getByRole('button', { name: 'Mark as read' }));

    await waitFor(() => expect(apiMock.getFeeds.mock.calls.length).toBe(feedsCalls + 1));
    expect(apiMock.getFolders.mock.calls.length).toBe(foldersCalls + 1);
    expect(apiMock.updateArticle).toHaveBeenCalledOnce();
    expect(apiMock.updateArticle).toHaveBeenCalledWith(1, { isRead: true });
    expect(apiMock.getArticles).toHaveBeenCalledOnce();
    expect(within(articleCards()[0] as HTMLElement).getByRole('button', { name: 'Mark as unread' })).toBeTruthy();
  });

  it('keeps an unsaved article visible in the saved view without reloading the list', async () => {
    const user = userEvent.setup();
    apiMock.getArticles.mockResolvedValue({ items: [article(5, { isSaved: true })], hasMore: false, nextCursor: null });
    apiMock.updateArticle.mockResolvedValue(article(5, { isSaved: false }));
    render(<App />);

    await user.click(await screen.findByRole('button', { name: /^Read later/ }));
    await waitFor(() => expect(apiMock.getArticles).toHaveBeenCalledTimes(2));
    expect((apiMock.getArticles.mock.calls[1][0] as URLSearchParams).get('filter')).toBe('saved');
    await waitFor(() => expect(articleCards()).toHaveLength(1));
    await user.click(within(articleCards()[0] as HTMLElement).getByRole('button', { name: 'Remove from saved' }));

    await waitFor(() => expect(apiMock.updateArticle).toHaveBeenCalledWith(5, { isSaved: false }));
    await waitFor(() => expect(within(articleCards()[0] as HTMLElement).getByRole('button', { name: 'Save for later' })).toBeTruthy());
    expect(apiMock.updateArticle).toHaveBeenCalledOnce();
    expect(apiMock.getArticles).toHaveBeenCalledTimes(2);
  });

  it('rolls back the local read state when the update fails', async () => {
    const user = userEvent.setup();
    apiMock.getArticles.mockResolvedValue({ items: [article(1)], hasMore: false, nextCursor: null });
    apiMock.updateArticle.mockRejectedValue(new Error('The article could not be updated.'));
    render(<App />);

    await waitFor(() => expect(articleCards()).toHaveLength(1));
    await user.click(within(articleCards()[0] as HTMLElement).getByRole('button', { name: 'Mark as read' }));

    expect(await screen.findByText('The article could not be updated.')).toBeTruthy();
    expect(within(articleCards()[0] as HTMLElement).getByRole('button', { name: 'Mark as read' })).toBeTruthy();
    expect(apiMock.getArticles).toHaveBeenCalledOnce();
  });
});

describe('cancelled article loads', () => {
  it('aborts the previous view request when the filter changes without showing an error', async () => {
    const user = userEvent.setup();
    const signals: AbortSignal[] = [];
    apiMock.getArticles.mockImplementation((params: URLSearchParams, signal: AbortSignal) => {
      signals.push(signal);
      if (params.get('filter') === 'saved') return Promise.resolve({ items: [article(9, { isSaved: true })], hasMore: false, nextCursor: null });
      return new Promise((_resolve, reject) => {
        signal.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')));
      });
    });
    render(<App />);

    await waitFor(() => expect(signals).toHaveLength(1));
    await user.click(await screen.findByRole('button', { name: /^Read later/ }));

    await waitFor(() => expect(articleCards()).toHaveLength(1));
    expect(signals[0].aborted).toBe(true);
    expect(signals[1].aborted).toBe(false);
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
