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

/** The "Remember me on this device for offline use" choice. */
function chooseRemember(value: boolean) {
  window.localStorage.setItem('pulse.offlineRemember', String(value));
}

function rememberChoice(): string | null {
  return window.localStorage.getItem('pulse.offlineRemember');
}

/** Makes the page look like the installed app (standalone display mode). */
function runInstalled() {
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: query === '(display-mode: standalone)', media: query }));
}

/** A server with a signed-in reader and an empty library. */
function signedInServer() {
  return vi.fn((input: string) => {
    const path = new URL(input, 'http://localhost').pathname;
    if (path === '/api/auth/me') return json(reader);
    if (path === '/api/auth/logout') return Promise.resolve(new Response(null, { status: 204 }));
    if (path === '/api/articles') return json({ items: [], hasMore: false, nextCursor: null });
    return json([]);
  });
}

beforeEach(() => window.localStorage.clear());

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('offline reading', () => {
  it('opens the reader for the remembered account when the network is unavailable', async () => {
    chooseRemember(true);
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

  it('forgets the remember choice when the session check reports no session', async () => {
    chooseRemember(true);
    remember(reader);
    vi.stubGlobal('fetch', vi.fn(() => json({ code: 'auth.sign_in_required', error: 'Please sign in.' }, 401)));
    render(<App />);

    await screen.findByRole('heading', { name: 'Welcome back' });
    // The next person to sign in on this browser starts from the default.
    await waitFor(() => expect(rememberChoice()).toBeNull());
    expect((screen.getByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement).checked).toBe(false);
  });

  it('forgets the remember choice when a request reports the session expired', async () => {
    chooseRemember(true);
    const stored = stubCaches(['pulse-api-v1']);
    vi.stubGlobal('fetch', vi.fn((input: string) => {
      const path = new URL(input, 'http://localhost').pathname;
      if (path === '/api/auth/me') return json(reader);
      return json({ code: 'auth.sign_in_required', error: 'Please sign in.' }, 401);
    }));
    render(<App />);

    expect(await screen.findByText('Your session has expired. Please sign in again.')).toBeTruthy();
    await waitFor(() => expect(stored.size).toBe(0));
    expect(remembered()).toBeNull();
    expect(rememberChoice()).toBeNull();
    expect((screen.getByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement).checked).toBe(false);
  });

  it('drops stored data and the remember choice on sign-out', async () => {
    const user = userEvent.setup();
    chooseRemember(true);
    const stored = stubCaches(['pulse-api-v1', 'pulse-shell-abc']);
    vi.stubGlobal('fetch', signedInServer());
    render(<App />);

    await screen.findByRole('button', { name: 'Create folder' });
    expect(remembered()).toMatchObject({ id: 3 });
    expect([...stored]).toEqual(['pulse-api-v1', 'pulse-shell-abc']);
    await user.click(screen.getAllByRole('button', { name: 'Sign out' })[0]);
    await screen.findByRole('heading', { name: 'Welcome back' });
    expect([...stored]).toEqual(['pulse-shell-abc']);
    expect(remembered()).toBeNull();
    // The next person to sign in on this browser starts from the default again.
    expect(rememberChoice()).toBeNull();
    expect((screen.getByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement).checked).toBe(false);
  });

  it('drops the previous account’s data when another account signs in', async () => {
    const user = userEvent.setup();
    chooseRemember(true);
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
    expect((screen.getByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement).checked).toBe(true);
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    await screen.findByRole('button', { name: 'Create folder' });
    expect(stored.size).toBe(0);
    expect(remembered()).toMatchObject({ id: 4, email: 'other@example.com' });
  });
});

describe('remembering this device for offline use', () => {
  const rememberBox = () => screen.getByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement;

  async function signInThroughForm(remember: boolean) {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: 'Welcome back' });
    await user.type(screen.getByLabelText('Email'), 'reader@example.com');
    await user.type(screen.getByLabelText('Password'), 'secret-password');
    if (rememberBox().checked !== remember) await user.click(rememberBox());
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await screen.findByRole('button', { name: 'Create folder' });
    return user;
  }

  function signInServer() {
    let signedIn = false;
    return vi.fn((input: string, init?: RequestInit) => {
      const path = new URL(input, 'http://localhost').pathname;
      if (path === '/api/auth/login' && init?.method === 'POST') {
        signedIn = true;
        return json(reader);
      }
      if (path === '/api/auth/me') return signedIn ? json(reader) : json({ code: 'auth.sign_in_required', error: 'Please sign in.' }, 401);
      if (path === '/api/articles') return json({ items: [], hasMore: false, nextCursor: null });
      return json([]);
    });
  }

  it('is off by default in a browser tab: the account is not remembered', async () => {
    vi.stubGlobal('fetch', signInServer());
    render(<App />);
    await screen.findByRole('heading', { name: 'Welcome back' });
    expect(rememberBox().checked).toBe(false);
    cleanup();

    await signInThroughForm(false);
    expect(remembered()).toBeNull();
    expect(rememberChoice()).toBe('false');
  });

  it('is on by default in the installed app', async () => {
    runInstalled();
    vi.stubGlobal('fetch', signInServer());
    await signInThroughForm(true);
    expect(remembered()).toMatchObject({ id: 3, email: 'reader@example.com' });
  });

  it('remembers the account when chosen on the sign-in form', async () => {
    vi.stubGlobal('fetch', signInServer());
    await signInThroughForm(true);
    expect(remembered()).toMatchObject({ id: 3, email: 'reader@example.com' });
    expect(rememberChoice()).toBe('true');
  });

  it('deletes data of an earlier visit before the session check when not remembering', async () => {
    remember(reader);
    const stored = stubCaches(['pulse-api-v1', 'pulse-shell-abc']);
    const atSessionCheck: string[][] = [];
    const server = signedInServer();
    vi.stubGlobal('fetch', vi.fn((input: string) => {
      if (input === '/api/auth/me') atSessionCheck.push([...stored]);
      return server(input);
    }));
    render(<App />);

    await screen.findByRole('button', { name: 'Create folder' });
    expect(atSessionCheck).toEqual([['pulse-shell-abc']]);
    expect(remembered()).toBeNull();
  });

  it('keeps stored data at start when remembering', async () => {
    chooseRemember(true);
    remember(reader);
    const stored = stubCaches(['pulse-api-v1', 'pulse-shell-abc']);
    vi.stubGlobal('fetch', signedInServer());
    render(<App />);

    await screen.findByRole('button', { name: 'Create folder' });
    expect([...stored]).toEqual(['pulse-api-v1', 'pulse-shell-abc']);
    expect(remembered()).toMatchObject({ id: 3 });
  });

  it('turning it off in the profile forgets the account and stored articles at once', async () => {
    const user = userEvent.setup();
    chooseRemember(true);
    const stored = stubCaches(['pulse-api-v1', 'pulse-shell-abc']);
    vi.stubGlobal('fetch', signedInServer());
    render(<App />);
    await screen.findByRole('button', { name: 'Create folder' });
    expect(remembered()).toMatchObject({ id: 3 });

    await user.click(screen.getAllByRole('button', { name: 'Edit profile' })[0]);
    const box = await screen.findByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement;
    expect(box.checked).toBe(true);
    await user.click(box);

    expect(box.checked).toBe(false);
    await waitFor(() => expect([...stored]).toEqual(['pulse-shell-abc']));
    expect(remembered()).toBeNull();
    expect(rememberChoice()).toBe('false');
  });

  it('turning it on in the profile remembers the account at once', async () => {
    const user = userEvent.setup();
    vi.stubGlobal('fetch', signedInServer());
    render(<App />);
    await screen.findByRole('button', { name: 'Create folder' });
    expect(remembered()).toBeNull();

    await user.click(screen.getAllByRole('button', { name: 'Edit profile' })[0]);
    const box = await screen.findByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement;
    expect(box.checked).toBe(false);
    await user.click(box);

    await waitFor(() => expect(remembered()).toMatchObject({ id: 3, email: 'reader@example.com' }));
    expect(rememberChoice()).toBe('true');
  });

  it('shows the stored choice and an error when the profile cannot save it', async () => {
    const user = userEvent.setup();
    vi.stubGlobal('fetch', signedInServer());
    render(<App />);
    await screen.findByRole('button', { name: 'Create folder' });

    const setItem = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === 'pulse.offlineRemember') throw new DOMException('Quota exceeded', 'QuotaExceededError');
      setItem.call(this, key, value);
    });
    await user.click(screen.getAllByRole('button', { name: 'Edit profile' })[0]);
    const box = await screen.findByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement;
    expect(box.checked).toBe(false);
    await user.click(box);

    expect(await screen.findByText('Your choice could not be saved on this device. Check whether the browser allows this site to store data.')).toBeTruthy();
    expect(box.checked).toBe(false);
    expect(rememberChoice()).toBeNull();
    expect(remembered()).toBeNull();
  });
});
