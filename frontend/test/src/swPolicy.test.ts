import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  API_CACHE,
  API_CACHE_MAX_ENTRIES,
  API_NETWORK_TIMEOUT_MS,
  SHELL_CACHE_PREFIX,
  clearsApiCache,
  entriesToEvict,
  isCacheableApiResponse,
  isObsoleteCache,
  networkFirstWithTimeout,
  requestStrategy,
} from '../../src/sw/policy';
import { API_CACHE_PREFIX } from '../../src/offlineData';

const origin = 'https://pulse.example';

function strategy(method: string, path: string, mode: RequestMode = 'cors', requestOrigin = origin) {
  return requestStrategy({ method, url: `${requestOrigin}${path}`, mode }, origin);
}

describe('service worker request strategy', () => {
  it('serves article lists and the library network-first with an offline fallback', () => {
    expect(strategy('GET', '/api/articles?filter=all&limit=20')).toBe('api');
    expect(strategy('GET', '/api/articles?filter=unread&before=abc')).toBe('api');
    expect(strategy('GET', '/api/articles/42')).toBe('api');
    expect(strategy('GET', '/api/feeds')).toBe('api');
    expect(strategy('GET', '/api/folders')).toBe('api');
  });

  it('never caches authentication, administration or other API endpoints', () => {
    for (const path of [
      '/api/auth/me', '/api/auth/login', '/api/auth/logout', '/api/auth/profile',
      '/api/admin/settings', '/api/admin/users', '/api/admin/feed-requests', '/api/admin/blocked-articles?limit=100',
      '/api/opml', '/api/refresh', '/api/health/ready', '/api/feeds/3', '/api/folders/2', '/api/articlesx',
    ]) {
      expect(strategy('GET', path), path).toBe('network');
    }
    // A navigation to an API URL (the OPML download link) is not the app shell.
    expect(strategy('GET', '/api/opml', 'navigate')).toBe('network');
  });

  it('never caches requests that change data', () => {
    for (const method of ['POST', 'PATCH', 'PUT', 'DELETE']) {
      expect(strategy(method, '/api/articles/mark-read')).toBe('network');
      expect(strategy(method, '/api/articles/7')).toBe('network');
      expect(strategy(method, '/')).toBe('network');
    }
  });

  it('serves the app shell from the precache', () => {
    expect(strategy('GET', '/', 'navigate')).toBe('navigation');
    expect(strategy('GET', '/saved', 'navigate')).toBe('navigation');
    expect(strategy('GET', '/assets/index-abc123.js')).toBe('asset');
    expect(strategy('GET', '/favicon.svg')).toBe('static');
    expect(strategy('GET', '/theme-init.js')).toBe('static');
  });

  it('leaves the service worker script and other origins alone', () => {
    expect(strategy('GET', '/sw.js')).toBe('network');
    expect(strategy('GET', '/assets/x.js', 'cors', 'https://cdn.example')).toBe('network');
    expect(strategy('GET', '/api/articles', 'cors', 'https://other.example')).toBe('network');
  });
});

describe('API cache privacy', () => {
  it('clears cached API data when the session changes or is rejected', () => {
    expect(clearsApiCache('POST', '/api/auth/login', null)).toBe(true);
    expect(clearsApiCache('POST', '/api/auth/logout', null)).toBe(true);
    expect(clearsApiCache('GET', '/api/articles', 401)).toBe(true);
    expect(clearsApiCache('GET', '/api/feeds', 401)).toBe(true);
    expect(clearsApiCache('GET', '/api/articles', 200)).toBe(false);
    expect(clearsApiCache('GET', '/api/articles', 503)).toBe(false);
    expect(clearsApiCache('POST', '/api/auth/password', null)).toBe(false);
  });

  it('stores only successful JSON responses', () => {
    const json = { 'content-type': 'application/json; charset=utf-8' };
    expect(isCacheableApiResponse({ status: 200, type: 'basic', headers: new Headers(json) })).toBe(true);
    expect(isCacheableApiResponse({ status: 206, type: 'basic', headers: new Headers(json) })).toBe(false);
    expect(isCacheableApiResponse({ status: 304, type: 'basic', headers: new Headers(json) })).toBe(false);
    expect(isCacheableApiResponse({ status: 401, type: 'basic', headers: new Headers(json) })).toBe(false);
    expect(isCacheableApiResponse({ status: 500, type: 'basic', headers: new Headers(json) })).toBe(false);
    expect(isCacheableApiResponse({ status: 200, type: 'basic', headers: new Headers({ 'content-type': 'text/html' }) })).toBe(false);
    expect(isCacheableApiResponse({ status: 200, type: 'opaque', headers: new Headers(json) })).toBe(false);
    expect(isCacheableApiResponse({ status: 200, type: 'basic', headers: new Headers({ ...json, 'cache-control': 'no-store' }) })).toBe(false);
  });

  it('keeps only the most recently stored responses', () => {
    const keys = Array.from({ length: API_CACHE_MAX_ENTRIES + 3 }, (_, index) => `k${index}`);
    expect(entriesToEvict(keys)).toEqual(['k0', 'k1', 'k2']);
    expect(entriesToEvict(keys.slice(0, API_CACHE_MAX_ENTRIES))).toEqual([]);
  });

  it('uses cache names the page can find to clear them', () => {
    expect(API_CACHE.startsWith(API_CACHE_PREFIX)).toBe(true);
    expect(SHELL_CACHE_PREFIX.startsWith(API_CACHE_PREFIX)).toBe(false);
  });

  it('removes shell caches of older versions only', () => {
    expect(isObsoleteCache(`${SHELL_CACHE_PREFIX}old`, `${SHELL_CACHE_PREFIX}new`)).toBe(true);
    expect(isObsoleteCache(`${SHELL_CACHE_PREFIX}new`, `${SHELL_CACHE_PREFIX}new`)).toBe(false);
    expect(isObsoleteCache(API_CACHE, `${SHELL_CACHE_PREFIX}new`)).toBe(false);
    expect(isObsoleteCache('someone-else', `${SHELL_CACHE_PREFIX}new`)).toBe(false);
  });
});

describe('network-first API requests with a time limit', () => {
  afterEach(() => vi.useRealTimers());

  /** A network request the test settles by hand. */
  function pendingNetwork() {
    let resolve!: (value: string) => void;
    let reject!: (reason: unknown) => void;
    const promise = new Promise<string>((res, rej) => { resolve = res; reject = rej; });
    return { promise, resolve, reject };
  }

  function observe(promise: Promise<string>) {
    const state: { value?: string; error?: unknown; settled: boolean } = { settled: false };
    promise.then((value) => { state.value = value; state.settled = true; }, (error: unknown) => { state.error = error; state.settled = true; });
    return state;
  }

  const notAborted = () => false;

  it('waits a few seconds before it gives up on the network', () => {
    expect(API_NETWORK_TIMEOUT_MS).toBeGreaterThanOrEqual(3000);
    expect(API_NETWORK_TIMEOUT_MS).toBeLessThanOrEqual(5000);
  });

  it('returns the network response when it arrives in time, without reading the cache', async () => {
    vi.useFakeTimers();
    const network = pendingNetwork();
    const cached = vi.fn(async () => 'stored');
    const state = observe(networkFirstWithTimeout(network.promise, cached, { timeoutMs: API_NETWORK_TIMEOUT_MS, aborted: notAborted }));

    await vi.advanceTimersByTimeAsync(API_NETWORK_TIMEOUT_MS - 1);
    network.resolve('fresh');
    await vi.advanceTimersByTimeAsync(10_000);
    expect(state).toEqual({ value: 'fresh', settled: true });
    expect(cached).not.toHaveBeenCalled();
  });

  it('returns the stored response when the network hangs past the time limit', async () => {
    vi.useFakeTimers();
    const network = pendingNetwork();
    const state = observe(networkFirstWithTimeout(network.promise, async () => 'stored', { timeoutMs: API_NETWORK_TIMEOUT_MS, aborted: notAborted }));

    await vi.advanceTimersByTimeAsync(API_NETWORK_TIMEOUT_MS - 1);
    expect(state.settled).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(state).toEqual({ value: 'stored', settled: true });
    // A late network response does not change the answer already given.
    network.resolve('fresh');
    await vi.advanceTimersByTimeAsync(0);
    expect(state.value).toBe('stored');
  });

  it('keeps waiting for the network when nothing is stored', async () => {
    vi.useFakeTimers();
    const network = pendingNetwork();
    const cached = vi.fn(async () => undefined);
    const state = observe(networkFirstWithTimeout(network.promise, cached, { timeoutMs: API_NETWORK_TIMEOUT_MS, aborted: notAborted }));

    await vi.advanceTimersByTimeAsync(10 * API_NETWORK_TIMEOUT_MS);
    expect(state.settled).toBe(false);
    expect(cached).toHaveBeenCalledTimes(1);
    network.resolve('slow but fine');
    await vi.advanceTimersByTimeAsync(0);
    expect(state).toEqual({ value: 'slow but fine', settled: true });
  });

  it('falls back to the stored response when the network fails, and fails without one', async () => {
    const failure = new TypeError('Failed to fetch');
    await expect(networkFirstWithTimeout(Promise.reject(failure), async () => 'stored', { timeoutMs: 10, aborted: notAborted })).resolves.toBe('stored');
    await expect(networkFirstWithTimeout(Promise.reject(failure), async () => undefined, { timeoutMs: 10, aborted: notAborted })).rejects.toBe(failure);
    // A failing cache lookup counts as nothing stored.
    await expect(networkFirstWithTimeout(Promise.reject(failure), () => Promise.reject(new Error('no storage')), { timeoutMs: 10, aborted: notAborted })).rejects.toBe(failure);
  });

  it('fails when the network fails after the time limit found nothing stored', async () => {
    vi.useFakeTimers();
    const network = pendingNetwork();
    const failure = new TypeError('Failed to fetch');
    const state = observe(networkFirstWithTimeout(network.promise, async () => undefined, { timeoutMs: API_NETWORK_TIMEOUT_MS, aborted: notAborted }));
    await vi.advanceTimersByTimeAsync(API_NETWORK_TIMEOUT_MS);
    network.reject(failure);
    await vi.advanceTimersByTimeAsync(0);
    expect(state).toEqual({ error: failure, settled: true });
  });

  it('does not answer an aborted request from the cache', async () => {
    const abort = new DOMException('The operation was aborted.', 'AbortError');
    const cached = vi.fn(async () => 'stored');
    await expect(networkFirstWithTimeout(Promise.reject(abort), cached, { timeoutMs: 10, aborted: () => true })).rejects.toBe(abort);
    expect(cached).not.toHaveBeenCalled();
  });
});
