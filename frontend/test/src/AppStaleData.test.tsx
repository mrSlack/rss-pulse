import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../../src/App';

// The service worker answers a slow API request with a stored copy marked
// `X-Pulse-Cache: stale` and tells the page when the network answer arrives
// later (src/sw/sw.ts). These tests play the worker's part.

const reader = { id: 3, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null };

function article(isRead: boolean) {
  return {
    id: 11, feedId: 1, feedTitle: 'Daily', feedUrl: 'https://example.com/feed.xml', title: 'Headline',
    url: 'https://example.com/a', summary: 'Summary', publishedAt: '2026-10-30T10:00:00Z', isRead, isSaved: false,
  };
}

function json(body: unknown, { status = 200, stale = false } = {}) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (stale) headers['X-Pulse-Cache'] = 'stale';
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers }));
}

/** Stands in for navigator.serviceWorker so the test can send the worker's messages. */
function fakeServiceWorker() {
  const container = new EventTarget();
  Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: container });
  return (data: unknown) => act(() => { container.dispatchEvent(new MessageEvent('message', { data })); });
}

/** A signed-in reader whose article list is first answered from the worker's cache. */
function server() {
  let stale = true;
  const fetch = vi.fn((input: string) => {
    const path = new URL(input, 'http://localhost').pathname;
    if (path === '/api/auth/me') return json(reader);
    if (path === '/api/articles') return json({ items: [article(!stale)], hasMore: false, nextCursor: null }, { stale });
    return json([]);
  });
  return { fetch, answerFresh: () => { stale = false; } };
}

const articleRequests = (fetch: ReturnType<typeof vi.fn>) => fetch.mock.calls.filter(([input]) => new URL(String(input), 'http://localhost').pathname === '/api/articles').length;

beforeEach(() => window.localStorage.clear());

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  delete (navigator as { serviceWorker?: unknown }).serviceWorker;
});

describe('data answered from the cache while online', () => {
  it('says that the articles shown are saved copies', async () => {
    fakeServiceWorker();
    const { fetch } = server();
    vi.stubGlobal('fetch', fetch);
    render(<App />);

    await screen.findByText('Headline');
    expect((await screen.findByRole('status')).textContent).toBe('The server is not responding. Showing articles saved earlier.');
  });

  it('loads the fresh data once the network answer arrives and clears the notice', async () => {
    const send = fakeServiceWorker();
    const { fetch, answerFresh } = server();
    vi.stubGlobal('fetch', fetch);
    render(<App />);
    await screen.findByRole('status');
    const before = articleRequests(fetch);

    answerFresh();
    send({ type: 'pulse-api-fresh' });
    // Several late answers (list, feeds, folders) lead to one reload.
    send({ type: 'pulse-api-fresh' });

    await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
    expect(articleRequests(fetch)).toBe(before + 1);
  });

  it('ends the session when the late network answer is a 401', async () => {
    const send = fakeServiceWorker();
    const { fetch } = server();
    vi.stubGlobal('fetch', fetch);
    render(<App />);
    await screen.findByText('Headline');

    send({ type: 'pulse-session-expired' });

    expect(await screen.findByRole('heading', { name: 'Welcome back' })).toBeTruthy();
    expect(screen.getByText('Your session has expired. Please sign in again.')).toBeTruthy();
  });
});
