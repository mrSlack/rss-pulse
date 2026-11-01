import { describe, expect, it } from 'vitest';
import {
  API_CACHE,
  API_CACHE_MAX_ENTRIES,
  SHELL_CACHE_PREFIX,
  clearsApiCache,
  entriesToEvict,
  isCacheableApiResponse,
  isObsoleteCache,
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
