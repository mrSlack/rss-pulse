import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../../src/App';

const reader = { id: 3, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null };
const otherReader = { id: 4, email: 'other@example.com', role: 'Reader', refreshIntervalMinutes: null };
const article = {
  id: 11, feedId: 1, feedTitle: 'Daily', feedUrl: 'https://example.com/feed.xml', title: 'Stored headline',
  url: 'https://example.com/a', summary: 'Summary', publishedAt: '2026-10-30T10:00:00Z', isRead: false, isSaved: false,
};

function json(body: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
}

/** An in-memory CacheStorage holding the given cache names. */
function stubCaches(names: string[]) {
  const stored = new Set(names);
  const storage = {
    keys: vi.fn(async () => [...stored]),
    delete: vi.fn(async (name: string) => stored.delete(name)),
  };
  vi.stubGlobal('caches', storage);
  return stored;
}

function remember(user: object) {
  window.localStorage.setItem('pulse.offlineUser', JSON.stringify(user));
}

function remembered(): unknown {
  return JSON.parse(window.localStorage.getItem('pulse.offlineUser') || 'null');
}

beforeEach(() => window.localStorage.clear());

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('offline reading', () => {
  it('opens the reader for the remembered account when the network is unavailable', async () => {
    remember(reader);
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    vi.stubGlobal('fetch', vi.fn((input: string) => {
      const path = new URL(input, 'http://localhost').pathname;
      // The service worker answers these from its cache; sign-in state is never cached.
      if (path === '/api/feeds' || path === '/api/folders') return json([]);
      if (path === '/api/articles') return json({ items: [article], hasMore: false, nextCursor: null });
      return Promise.reject(new TypeError('Failed to fetch'));
    }));
    render(<App />);

    expect(await screen.findByText('Stored headline')).toBeTruthy();
    expect(screen.getByRole('status').textContent).toBe('You are offline. Articles loaded earlier remain readable.');
    expect(remembered()).toMatchObject({ id: 3, email: 'reader@example.com' });
  });

  it('shows sign-in when offline and no account is remembered', async () => {
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Welcome back' })).toBeTruthy();
    expect(screen.getByRole('status').textContent).toBe('You are offline.');
  });

  it('announces when the connection drops and returns', async () => {
    vi.stubGlobal('fetch', vi.fn(() => json({ code: 'auth.sign_in_required', error: 'Please sign in.' }, 401)));
    const onLine = vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(true);
    render(<App />);
    await screen.findByRole('heading', { name: 'Welcome back' });
    expect(screen.queryByRole('status')).toBeNull();

    onLine.mockReturnValue(false);
    act(() => { window.dispatchEvent(new Event('offline')); });
    expect(screen.getByRole('status').textContent).toBe('You are offline.');
    onLine.mockReturnValue(true);
    act(() => { window.dispatchEvent(new Event('online')); });
    expect(screen.queryByRole('status')).toBeNull();
  });
});

describe('offline data privacy', () => {
  it('drops stored data when the server reports no session', async () => {
    remember(reader);
    const stored = stubCaches(['pulse-api-v1', 'pulse-shell-abc']);
    vi.stubGlobal('fetch', vi.fn(() => json({ code: 'auth.sign_in_required', error: 'Please sign in.' }, 401)));
    render(<App />);

    await screen.findByRole('heading', { name: 'Welcome back' });
    await waitFor(() => expect([...stored]).toEqual(['pulse-shell-abc']));
    expect(remembered()).toBeNull();
  });

  it('drops stored data on sign-out', async () => {
    const user = userEvent.setup();
    const stored = stubCaches(['pulse-api-v1', 'pulse-shell-abc']);
    vi.stubGlobal('fetch', vi.fn((input: string) => {
      const path = new URL(input, 'http://localhost').pathname;
      if (path === '/api/auth/me') return json(reader);
      if (path === '/api/auth/logout') return Promise.resolve(new Response(null, { status: 204 }));
      if (path === '/api/articles') return json({ items: [], hasMore: false, nextCursor: null });
      return json([]);
    }));
    render(<App />);

    await screen.findByRole('button', { name: 'Create folder' });
    expect(remembered()).toMatchObject({ id: 3 });
    await user.click(screen.getAllByRole('button', { name: 'Sign out' })[0]);
    await screen.findByRole('heading', { name: 'Welcome back' });
    expect([...stored]).toEqual(['pulse-shell-abc']);
    expect(remembered()).toBeNull();
  });

  it('drops the previous account’s data when another account signs in', async () => {
    const user = userEvent.setup();
    const stored = stubCaches(['pulse-api-v1']);
    vi.stubGlobal('fetch', vi.fn((input: string) => {
      const path = new URL(input, 'http://localhost').pathname;
      // The session probe cannot reach the server and no account is remembered yet.
      if (path === '/api/auth/me') return Promise.reject(new TypeError('Failed to fetch'));
      if (path === '/api/auth/login') return json(otherReader);
      if (path === '/api/articles') return json({ items: [], hasMore: false, nextCursor: null });
      return json([]);
    }));
    render(<App />);
    await screen.findByRole('heading', { name: 'Welcome back' });

    // Data of the account that used this browser before.
    remember(reader);
    await user.type(screen.getByLabelText('Email'), 'other@example.com');
    await user.type(screen.getByLabelText('Password'), 'secret-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    await screen.findByRole('button', { name: 'Create folder' });
    expect(stored.size).toBe(0);
    expect(remembered()).toMatchObject({ id: 4, email: 'other@example.com' });
  });
});
