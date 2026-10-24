import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../../src/App';

const apiMock = vi.hoisted(() => ({
  currentUser: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
  changePassword: vi.fn(),
  getFeeds: vi.fn(),
  getFolders: vi.fn(),
  getArticles: vi.fn(),
  createFeed: vi.fn(),
  createFolder: vi.fn(),
  updateFolder: vi.fn(),
  updateArticle: vi.fn(),
  refresh: vi.fn(),
  refreshStatus: vi.fn(),
  getAdminSettings: vi.fn(),
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
  apiMock.currentUser.mockRejectedValue(new Error('Please sign in to continue.'));
  apiMock.login.mockResolvedValue({ id: 3, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null });
  apiMock.logout.mockResolvedValue(undefined);
  apiMock.getFeeds.mockResolvedValue([]);
  apiMock.getFolders.mockResolvedValue([]);
  apiMock.getArticles.mockResolvedValue({ items: [], hasMore: false });
  apiMock.getAdminSettings.mockResolvedValue({ refreshIntervalMinutes: 15, sharedFeedTTLMinutes: 15, users: [] });
});

afterEach(() => cleanup());

describe('session-aware reader shell', () => {
  it('shows sign-in before loading the private library and returns to sign-in after logout', async () => {
    const user = userEvent.setup();
    const { container } = render(<App />);

    expect(await screen.findByRole('heading', { name: 'Welcome back' })).toBeTruthy();
    expect(screen.queryByRole('navigation', { name: 'Sources' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Administration' })).toBeNull();
    await user.type(screen.getByLabelText('Email'), 'reader@example.com');
    await user.type(screen.getByLabelText('Password'), 'a-long-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    await waitFor(() => expect(container.querySelector('.profile-menu-sidebar')?.textContent).toContain('reader@example.com'));
    expect(apiMock.login).toHaveBeenCalledWith('reader@example.com', 'a-long-password');
    const sidebarMenu = container.querySelector('.profile-menu-sidebar') as HTMLElement;
    await user.click(sidebarMenu.querySelector(':scope > summary')!);
    await user.click(within(sidebarMenu).getByRole('button', { name: 'Sign out' }));
    expect(await screen.findByRole('heading', { name: 'Welcome back' })).toBeTruthy();
    await waitFor(() => expect(apiMock.logout).toHaveBeenCalledOnce());
  });

  it('shows the administration destination to Administrators only', async () => {
    const user = userEvent.setup();
    apiMock.currentUser.mockResolvedValue({ id: 1, email: 'admin@example.com', role: 'Administrator', refreshIntervalMinutes: null });
    const { container } = render(<App />);

    await waitFor(() => expect(container.querySelector('.profile-menu-sidebar > summary')).not.toBeNull());
    const sidebarMenu = container.querySelector('.profile-menu-sidebar') as HTMLElement;
    await user.click(sidebarMenu.querySelector(':scope > summary')!);
    await user.click(within(sidebarMenu).getByRole('button', { name: 'Administration' }));
    expect(await screen.findByRole('heading', { name: 'Administration' })).toBeTruthy();
  });
});
