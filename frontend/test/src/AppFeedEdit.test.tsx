import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../../src/App';
import type { Feed } from '../../src/api';

const apiMock = vi.hoisted(() => ({
  currentUser: vi.fn(),
  getFeeds: vi.fn(),
  getFolders: vi.fn(),
  getArticles: vi.fn(),
  updateFeed: vi.fn(),
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

const feed: Feed = {
  id: 1,
  title: 'Example feed',
  url: 'https://example.com/feed.xml',
  folderId: null,
  folderName: null,
  unreadCount: 0,
  lastCheckedAt: null,
  lastError: null,
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
  apiMock.currentUser.mockResolvedValue({ id: 3, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null });
  apiMock.getFeeds.mockResolvedValue([feed]);
  apiMock.getFolders.mockResolvedValue([]);
  apiMock.getArticles.mockResolvedValue({ items: [], hasMore: false, nextCursor: null });
  apiMock.refreshStatus.mockResolvedValue({ running: false });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function editFeed(url: string) {
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole('button', { name: 'Edit Example feed' }));
  const urlInput = screen.getByLabelText('RSS feed URL');
  await user.clear(urlInput);
  await user.type(urlInput, url);
  await user.click(screen.getByRole('button', { name: /Save changes/ }));
}

describe('feed editing', () => {
  it('follows the background refresh after the feed URL changes', async () => {
    apiMock.updateFeed.mockResolvedValue({ ...feed, url: 'https://example.com/other.xml' });
    await editFeed('https://example.com/other.xml');
    await waitFor(() => expect(apiMock.refreshStatus).toHaveBeenCalled(), { timeout: 8000 });
    expect(apiMock.refresh).not.toHaveBeenCalled();
  }, 15000);

  it('does not wait for a refresh when only the name changes', async () => {
    apiMock.updateFeed.mockResolvedValue(feed);
    await editFeed(feed.url);
    await waitFor(() => expect(apiMock.updateFeed).toHaveBeenCalled());
    await new Promise((resolve) => window.setTimeout(resolve, 900));
    expect(apiMock.refreshStatus).not.toHaveBeenCalled();
  }, 15000);
});
