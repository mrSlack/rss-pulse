import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
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
  markArticlesRead: vi.fn(),
  importOpml: vi.fn(),
  refresh: vi.fn(),
  refreshStatus: vi.fn(),
}));

vi.mock('../../src/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../src/api')>()),
  api: apiMock,
}));

class FakeIntersectionObserver {
  observe() {}
  disconnect() {}
}

function article(id: number, overrides: Partial<Article> = {}): Article {
  return {
    id,
    feedId: 1,
    feedTitle: 'Example feed',
    feedUrl: 'https://example.com/feed.xml',
    title: `Article ${id}`,
    url: `https://example.com/articles/${id}`,
    summary: `Summary ${id}`,
    publishedAt: `2026-10-31T12:${String(60 - id).padStart(2, '0')}:00.123456Z`,
    isRead: false,
    isSaved: false,
    ...overrides,
  };
}

function readerTitle() {
  return document.querySelector('.reading-article h2')?.textContent ?? null;
}

function cards() {
  return Array.from(document.querySelectorAll<HTMLElement>('.article-card'));
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
  apiMock.currentUser.mockResolvedValue({ id: 3, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null });
  apiMock.getFeeds.mockResolvedValue([]);
  apiMock.getFolders.mockResolvedValue([]);
  apiMock.getArticles.mockResolvedValue({ items: [article(1), article(2, { isRead: true }), article(3)], hasMore: false, nextCursor: null });
  apiMock.updateArticle.mockImplementation((id: number, update: Partial<Article>) => Promise.resolve(article(id, update)));
  apiMock.markArticlesRead.mockResolvedValue({ marked: 2 });
  apiMock.refresh.mockResolvedValue({ message: 'started' });
  apiMock.refreshStatus.mockResolvedValue({ running: false });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('mark all as read', () => {
  it('marks the loaded view up to its ingestion bound and updates the list without reloading it', async () => {
    const user = userEvent.setup();
    apiMock.getArticles.mockResolvedValue({ items: [article(1), article(2, { isRead: true }), article(3)], hasMore: false, nextCursor: null, latestArticleId: 3 });
    render(<App />);
    await waitFor(() => expect(cards()).toHaveLength(3));
    const feedsCalls = apiMock.getFeeds.mock.calls.length;

    await user.click(screen.getByRole('button', { name: 'Mark all as read' }));

    await waitFor(() => expect(apiMock.markArticlesRead).toHaveBeenCalledOnce());
    expect(apiMock.markArticlesRead).toHaveBeenCalledWith(
      { filter: 'all', feedId: null, folderId: null, q: '', since: null },
      { upToId: 3 },
    );
    await waitFor(() => expect(document.querySelectorAll('.article-card.unread')).toHaveLength(0));
    await waitFor(() => expect(apiMock.getFeeds.mock.calls.length).toBe(feedsCalls + 1));
    expect(apiMock.getArticles).toHaveBeenCalledOnce();
    expect((screen.getByRole('button', { name: 'Mark all as read' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('asks for confirmation when more articles exist than are loaded', async () => {
    const user = userEvent.setup();
    apiMock.getArticles.mockResolvedValue({ items: [article(1)], hasMore: true, nextCursor: 'next' });
    render(<App />);
    await waitFor(() => expect(cards()).toHaveLength(1));

    await user.click(screen.getByRole('button', { name: 'Mark all as read' }));
    const dialog = await screen.findByRole('dialog', { name: 'Mark all as read?' });
    expect(apiMock.markArticlesRead).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole('button', { name: 'Mark as read' }));

    await waitFor(() => expect(apiMock.markArticlesRead).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });
});

describe('keyboard shortcuts', () => {
  it('moves through articles and toggles their state', async () => {
    const user = userEvent.setup();
    render(<App />);
    await waitFor(() => expect(cards()).toHaveLength(3));

    await user.keyboard('j');
    await waitFor(() => expect(readerTitle()).toBe('Article 1'));
    await waitFor(() => expect(apiMock.updateArticle).toHaveBeenCalledWith(1, { isRead: true }));
    await user.keyboard('j');
    await waitFor(() => expect(readerTitle()).toBe('Article 2'));
    await user.keyboard('k');
    await waitFor(() => expect(readerTitle()).toBe('Article 1'));

    await user.keyboard('m');
    await waitFor(() => expect(apiMock.updateArticle).toHaveBeenCalledWith(1, { isRead: false }));
    await user.keyboard('s');
    await waitFor(() => expect(apiMock.updateArticle).toHaveBeenCalledWith(1, { isSaved: true }));

    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    await user.keyboard('o');
    expect(open).toHaveBeenCalledWith('https://example.com/articles/1', '_blank', 'noopener,noreferrer');
    open.mockRestore();
  });

  it('ignores keys typed into fields or with modifiers and focuses search with /', async () => {
    const user = userEvent.setup();
    render(<App />);
    await waitFor(() => expect(cards()).toHaveLength(3));

    await user.keyboard('/');
    // jsdom lays nothing out, so the narrow-screen search panel opens.
    await waitFor(() => expect(document.activeElement?.getAttribute('aria-label')).toBe('Search articles'));
    const search = document.activeElement as HTMLInputElement;
    await user.keyboard('jk');
    expect(search.value).toBe('jk');
    await user.click(document.body);
    await user.keyboard('{Control>}j{/Control}');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(apiMock.updateArticle).not.toHaveBeenCalled();
  });

  it('refreshes feeds with r', async () => {
    const user = userEvent.setup();
    render(<App />);
    await waitFor(() => expect(cards()).toHaveLength(3));
    await user.keyboard('r');
    await waitFor(() => expect(apiMock.refresh).toHaveBeenCalledOnce());
  });

  it('opens an accessible help dialog with ? and closes it with Escape', async () => {
    const user = userEvent.setup();
    render(<App />);
    await waitFor(() => expect(cards()).toHaveLength(3));

    await user.keyboard('?');
    const dialog = await screen.findByRole('dialog', { name: 'Keyboard shortcuts' });
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(within(dialog).getByText('Open the next article')).toBeTruthy();
    expect(dialog.contains(document.activeElement)).toBe(true);
    await user.tab();
    expect(dialog.contains(document.activeElement)).toBe(true);

    // Shortcuts are off while the dialog is open.
    await user.keyboard('j');
    expect(readerTitle()).toBeNull();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Keyboard shortcuts' })).toBeNull());
  });
});

describe('OPML import', () => {
  it('uploads the chosen file from the profile menu and summarizes the result', async () => {
    const user = userEvent.setup();
    apiMock.importOpml.mockResolvedValue({ subscribed: 2, requested: 1, skipped: 0, failed: [{ url: 'ftp://bad.example/feed', reason: 'feed.url_invalid' }] });
    const { container } = render(<App />);
    await waitFor(() => expect(cards()).toHaveLength(3));
    const menu = container.querySelector('.profile-menu-sidebar') as HTMLElement;
    await user.click(menu.querySelector(':scope > summary')!);
    expect(within(menu).getByRole('link', { name: 'Export subscriptions' }).getAttribute('href')).toBe('/api/opml');
    await user.click(within(menu).getByRole('button', { name: 'Import subscriptions' }));

    const dialog = await screen.findByRole('dialog', { name: 'Import subscriptions' });
    const file = new File(['<opml version="2.0"><body/></opml>'], 'feeds.opml', { type: 'text/x-opml' });
    await user.upload(within(dialog).getByLabelText('OPML file'), file);
    const feedsCalls = apiMock.getFeeds.mock.calls.length;
    await user.click(within(dialog).getByRole('button', { name: 'Import' }));

    await waitFor(() => expect(apiMock.importOpml).toHaveBeenCalledWith(file));
    const summary = await within(dialog).findByRole('status');
    expect(summary.textContent).toContain('2 feeds added to your library.');
    expect(summary.textContent).toContain('1 feed sent for administrator approval.');
    expect(summary.textContent).toContain('ftp://bad.example/feed — Enter a valid HTTP or HTTPS feed URL.');
    await waitFor(() => expect(apiMock.getFeeds.mock.calls.length).toBe(feedsCalls + 1));
  });
});
