import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AdminPanel } from '../../src/AdminPanel';

const apiMock = vi.hoisted(() => ({
  getAdminSettings: vi.fn(),
  getFeedRequests: vi.fn(),
  approveFeedRequest: vi.fn(),
  rejectFeedRequest: vi.fn(),
  getStopWords: vi.fn(),
  addStopWord: vi.fn(),
  deleteStopWord: vi.fn(),
  getBlockedFeedArticles: vi.fn(),
  allowBlockedFeedArticle: vi.fn(),
  rejectBlockedFeedArticle: vi.fn(),
  updateAdminSettings: vi.fn(),
  createUser: vi.fn(),
  updateUser: vi.fn(),
}));

vi.mock('../../src/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../src/api')>()),
  api: apiMock,
}));

beforeEach(() => {
  vi.resetAllMocks();
  apiMock.getAdminSettings.mockResolvedValue({
    refreshIntervalMinutes: 15,
    sharedFeedTTLMinutes: 15,
    users: [
      { id: 1, email: 'admin@example.com', role: 'Administrator', refreshIntervalMinutes: null },
      { id: 2, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null },
    ],
  });
  apiMock.getFeedRequests.mockResolvedValue([]);
  apiMock.approveFeedRequest.mockResolvedValue(undefined);
  apiMock.rejectFeedRequest.mockResolvedValue(undefined);
  apiMock.getStopWords.mockResolvedValue([]);
  apiMock.addStopWord.mockImplementation(async (term: string) => ({ id: 99, term: term.toLowerCase().trim(), createdAt: '2026-10-26T09:00:00Z' }));
  apiMock.deleteStopWord.mockResolvedValue(undefined);
  apiMock.getBlockedFeedArticles.mockResolvedValue([]);
  apiMock.updateAdminSettings.mockResolvedValue({ refreshIntervalMinutes: 30, sharedFeedTTLMinutes: 45 });
  apiMock.createUser.mockResolvedValue({ id: 3, email: 'new@example.com', role: 'Reader', refreshIntervalMinutes: null });
  apiMock.updateUser.mockResolvedValue({ id: 2, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: 60 });
});

afterEach(() => cleanup());

describe('AdminPanel', () => {
  it('labels saved schedule values as inactive and shared TTL as active', async () => {
    render(<AdminPanel />);
    expect(await screen.findByRole('heading', { name: 'Administration' })).toBeTruthy();
    expect(screen.getByText('Not active yet')).toBeTruthy();
    expect(screen.getByText(/Applies to shared sources/i)).toBeTruthy();
    expect(screen.getByLabelText('Scheduler interval, minutes')).toBeTruthy();
    expect(screen.getByLabelText('Shared source TTL, minutes')).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Users' })).toBeNull();
  });

  it('creates accounts, saves settings, and changes a user override', async () => {
    const user = userEvent.setup();
    render(<AdminPanel />);

    await user.click(await screen.findByRole('button', { name: 'Users' }));
    await user.type(await screen.findByLabelText('New user email'), 'new@example.com');
    await user.type(screen.getByLabelText('Initial password'), 'a-long-new-password');
    await user.click(screen.getByRole('button', { name: 'Create user' }));
    await waitFor(() => expect(apiMock.createUser).toHaveBeenCalledWith({ email: 'new@example.com', password: 'a-long-new-password', role: 'Reader' }));

    await user.click(screen.getByRole('button', { name: 'Settings' }));
    fireEvent.change(screen.getByLabelText('Scheduler interval, minutes'), { target: { value: '30' } });
    fireEvent.change(screen.getByLabelText('Shared source TTL, minutes'), { target: { value: '45' } });
    await user.click(screen.getByRole('button', { name: 'Save intervals' }));
    await waitFor(() => expect(apiMock.updateAdminSettings).toHaveBeenCalledWith({ refreshIntervalMinutes: 30, sharedFeedTTLMinutes: 45 }));

    await user.click(screen.getByRole('button', { name: 'Users' }));
    await user.selectOptions(screen.getByLabelText('Interval for reader@example.com'), '60');
    await waitFor(() => expect(apiMock.updateUser).toHaveBeenCalledWith(2, { refreshIntervalMinutes: 60 }));
  });

  it('allows administrators to approve pending feed URLs', async () => {
    const user = userEvent.setup();
    apiMock.getFeedRequests.mockResolvedValueOnce([{
      id: 12,
      url: 'https://example.test/rss',
      requestedBy: 'reader@example.com',
      folderId: null,
      folderName: null,
      title: '',
      createdAt: '2026-10-26T09:00:00Z',
    }]);
    render(<AdminPanel />);
    await user.click(await screen.findByRole('button', { name: 'Feed requests' }));
    expect(await screen.findByText('https://example.test/rss')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Approve' }));
    await waitFor(() => expect(apiMock.approveFeedRequest).toHaveBeenCalledWith(12));
    expect(await screen.findByText('Feed approved. It will be fetched after approval.')).toBeTruthy();
  });

  it('shows quarantine context and offers both decisions for pending and rejected rows only', async () => {
    const user = userEvent.setup();
    const base = { articleGuid: 'guid', articleUrl: 'https://news.example/story', feedTitle: 'News feed', feedUrl: 'https://news.example/rss', addedBy: 'reader@example.com', matchedTerm: 'phrase', firstBlockedAt: '2026-10-26T09:00:00Z', lastBlockedAt: '2026-10-26T09:00:00Z', summary: '<p>Summary context</p>', publishedAt: '2020-01-02T03:04:05Z', decidedAt: null };
    const initialRows = [
      { ...base, id: 1, articleTitle: 'Pending story', status: 'pending', canModerate: true },
      { ...base, id: 2, articleTitle: 'Rejected story', status: 'rejected', canModerate: true },
      { ...base, id: 3, articleTitle: 'Allowed story', status: 'allowed', canModerate: false },
      { ...base, id: 4, articleTitle: 'Legacy story', status: 'pending', canModerate: false, publishedAt: null, summary: '' },
    ];
    apiMock.getBlockedFeedArticles.mockResolvedValueOnce(initialRows)
      .mockResolvedValueOnce(initialRows.map((row) => row.id === 1 ? { ...row, status: 'rejected' } : row))
      .mockResolvedValueOnce(initialRows.map((row) => row.id === 1 ? { ...row, status: 'rejected' } : row.id === 2 ? { ...row, status: 'allowed', canModerate: false } : row));
    apiMock.allowBlockedFeedArticle.mockResolvedValue(undefined);
    apiMock.rejectBlockedFeedArticle.mockResolvedValue(undefined);
    render(<AdminPanel />);
    await user.click(await screen.findByRole('button', { name: 'Blocked log' }));
    const pending = screen.getByText('Pending story').closest('tr')!;
    const rejected = screen.getByText('Rejected story').closest('tr')!;
    expect(within(pending).getByText('Summary context')).toBeTruthy();
    expect(within(pending).getByText('News feed')).toBeTruthy();
    expect(within(pending).getByText('phrase')).toBeTruthy();
    expect(pending.querySelector('time[datetime="2020-01-02T03:04:05Z"]')).toBeTruthy();
    expect(within(pending).getByText('Pending review')).toBeTruthy();
    expect(within(rejected).getByText('Rejected')).toBeTruthy();
    for (const row of [pending, rejected]) {
      expect(within(row).getByRole('button', { name: 'Allow' })).toBeTruthy();
      expect(within(row).getByRole('button', { name: 'Reject' })).toBeTruthy();
    }
    for (const title of ['Allowed story', 'Legacy story']) {
      expect(within(screen.getByText(title).closest('tr')!).queryByRole('button')).toBeNull();
    }
    expect(screen.getByText('Legacy record: article content is unavailable for moderation.')).toBeTruthy();
    await user.click(within(pending).getByRole('button', { name: 'Reject' }));
    await waitFor(() => expect(apiMock.rejectBlockedFeedArticle).toHaveBeenCalledWith(1));
    expect(within(pending).getByText('Rejected')).toBeTruthy();
    await user.click(within(rejected).getByRole('button', { name: 'Allow' }));
    await waitFor(() => expect(apiMock.allowBlockedFeedArticle).toHaveBeenCalledWith(2));
    expect(within(rejected).getByText('Allowed')).toBeTruthy();
    expect(within(rejected).queryByRole('button')).toBeNull();
  });

  it('preserves the quarantine row and shows an API error when a decision fails', async () => {
    const user = userEvent.setup();
    apiMock.getBlockedFeedArticles.mockResolvedValueOnce([{ id: 7, articleTitle: 'Retry story', articleUrl: '', feedTitle: 'News', feedUrl: 'https://news.example/rss', matchedTerm: 'phrase', status: 'pending', canModerate: true, summary: '', publishedAt: null, lastBlockedAt: '2026-10-26T09:00:00Z' }]);
    apiMock.allowBlockedFeedArticle.mockRejectedValueOnce(new Error('Decision failed. Try again.'));
    render(<AdminPanel />);
    await user.click(await screen.findByRole('button', { name: 'Blocked log' }));
    await user.click(screen.getByRole('button', { name: 'Allow' }));
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', expect.stringContaining('Decision failed. Try again.'));
    expect(screen.getByText('Retry story')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Allow' })).toBeTruthy();
    expect(screen.getByText('Pending review')).toBeTruthy();
  });

  it('refetches the bounded queue after allowing an article to show the next pending item', async () => {
    const user = userEvent.setup();
    const row = { id: 7, articleTitle: 'Current story', articleUrl: '', feedTitle: 'News', feedUrl: 'https://news.example/rss', matchedTerm: 'phrase', status: 'pending', canModerate: true, summary: '', publishedAt: null, lastBlockedAt: '2026-10-26T09:00:00Z' };
    apiMock.getBlockedFeedArticles.mockResolvedValueOnce([row]).mockResolvedValueOnce([{ ...row, id: 8, articleTitle: 'Next pending story' }]);
    apiMock.allowBlockedFeedArticle.mockResolvedValueOnce(undefined);
    render(<AdminPanel />);
    await user.click(await screen.findByRole('button', { name: 'Blocked log' }));
    await user.click(screen.getByRole('button', { name: 'Allow' }));
    expect(await screen.findByText('Next pending story')).toBeTruthy();
    expect(screen.queryByText('Current story')).toBeNull();
    expect(screen.getByRole('button', { name: 'Allow' })).toBeTruthy();
  });

  it('keeps a successful decision when refreshing the queue fails', async () => {
    const user = userEvent.setup();
    apiMock.getBlockedFeedArticles.mockResolvedValueOnce([{ id: 7, articleTitle: 'Allowed story', articleUrl: '', feedTitle: 'News', feedUrl: 'https://news.example/rss', matchedTerm: 'phrase', status: 'pending', canModerate: true, summary: '', publishedAt: null, lastBlockedAt: '2026-10-26T09:00:00Z' }]).mockRejectedValueOnce(new Error('Network unavailable'));
    apiMock.allowBlockedFeedArticle.mockResolvedValueOnce(undefined);
    render(<AdminPanel />);
    await user.click(await screen.findByRole('button', { name: 'Blocked log' }));
    await user.click(screen.getByRole('button', { name: 'Allow' }));
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', expect.stringContaining('Decision saved, but the article list could not be refreshed. Reload to see the latest queue.'));
    expect(screen.getByText('Article allowed. It is now available to feed subscribers.')).toBeTruthy();
    expect(screen.getByText('Allowed')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Allow' })).toBeNull();
    expect(apiMock.allowBlockedFeedArticle).toHaveBeenCalledTimes(1);
  });

  it('manages blocked words and displays the blocked article audit log', async () => {
    const user = userEvent.setup();
    apiMock.getStopWords.mockResolvedValueOnce([{ id: 4, term: 'порно', createdAt: '2026-10-26T09:00:00Z' }]);
    apiMock.getBlockedFeedArticles.mockResolvedValueOnce([{
      id: 7,
      articleTitle: 'Blocked story',
      articleUrl: 'https://news.example/story',
      feedTitle: 'News feed',
      feedUrl: 'https://news.example/rss',
      addedBy: 'reader@example.com',
      matchedTerm: 'порно',
      firstBlockedAt: '2026-10-26T09:00:00Z',
      lastBlockedAt: '2026-10-26T09:00:00Z',
    }]);
    render(<AdminPanel />);

    await user.click(await screen.findByRole('button', { name: 'Blocked log' }));
    expect(await screen.findByRole('heading', { name: 'Blocked articles log' })).toBeTruthy();
    expect(screen.getAllByText('reader@example.com').length).toBeGreaterThan(0);
    expect(screen.getAllByText('порно').length).toBeGreaterThan(0);
    await user.click(screen.getByRole('button', { name: 'Blocked words' }));
    await user.type(screen.getByLabelText('Word or phrase to block'), 'Bad Phrase');
    await user.click(screen.getByRole('button', { name: 'Add to dictionary' }));
    await waitFor(() => expect(apiMock.addStopWord).toHaveBeenCalledWith('Bad Phrase'));
    expect(await screen.findByText('bad phrase')).toBeTruthy();

    const originalWordRow = screen.getAllByText('порно')[0].closest('tr');
    expect(originalWordRow).toBeTruthy();
    await user.click(within(originalWordRow!).getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(apiMock.deleteStopWord).toHaveBeenCalledWith(4));
  });
});
