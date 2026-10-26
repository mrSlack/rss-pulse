import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AdminPanel } from '../../src/AdminPanel';

const apiMock = vi.hoisted(() => ({
  getAdminSettings: vi.fn(),
  getFeedRequests: vi.fn(),
  approveFeedRequest: vi.fn(),
  rejectFeedRequest: vi.fn(),
  updateAdminSettings: vi.fn(),
  createUser: vi.fn(),
  updateUser: vi.fn(),
}));

vi.mock('../../src/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../src/api')>()),
  api: apiMock,
}));

beforeEach(() => {
  vi.clearAllMocks();
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
  apiMock.updateAdminSettings.mockResolvedValue({ refreshIntervalMinutes: 30, sharedFeedTTLMinutes: 45 });
  apiMock.createUser.mockResolvedValue({ id: 3, email: 'new@example.com', role: 'Reader', refreshIntervalMinutes: null });
  apiMock.updateUser.mockResolvedValue({ id: 2, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: 60 });
});

afterEach(() => cleanup());

describe('AdminPanel', () => {
  it('labels saved schedule values as inactive and shared TTL as active', async () => {
    render(<AdminPanel />);
    expect(await screen.findByRole('heading', { name: 'Administration' })).toBeTruthy();
    expect(screen.getByText(/do not change the refresh schedule yet/i)).toBeTruthy();
    expect(screen.getByText(/Applies to shared sources/i)).toBeTruthy();
    expect(screen.getByLabelText('Scheduler interval, minutes')).toBeTruthy();
    expect(screen.getByLabelText('Shared source TTL, minutes')).toBeTruthy();
  });

  it('creates accounts, saves settings, and changes a user override', async () => {
    const user = userEvent.setup();
    render(<AdminPanel />);

    await user.type(await screen.findByLabelText('New user email'), 'new@example.com');
    await user.type(screen.getByLabelText('Initial password'), 'a-long-new-password');
    await user.click(screen.getByRole('button', { name: 'Create user' }));
    await waitFor(() => expect(apiMock.createUser).toHaveBeenCalledWith({ email: 'new@example.com', password: 'a-long-new-password', role: 'Reader' }));

    fireEvent.change(screen.getByLabelText('Scheduler interval, minutes'), { target: { value: '30' } });
    fireEvent.change(screen.getByLabelText('Shared source TTL, minutes'), { target: { value: '45' } });
    await user.click(screen.getByRole('button', { name: 'Save intervals' }));
    await waitFor(() => expect(apiMock.updateAdminSettings).toHaveBeenCalledWith({ refreshIntervalMinutes: 30, sharedFeedTTLMinutes: 45 }));

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
    expect(await screen.findByText('https://example.test/rss')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Approve' }));
    await waitFor(() => expect(apiMock.approveFeedRequest).toHaveBeenCalledWith(12));
    expect(await screen.findByText('Feed approved. It will be fetched after approval.')).toBeTruthy();
  });
});
