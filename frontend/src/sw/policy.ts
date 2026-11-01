// Caching rules of the service worker (src/sw/sw.ts). Pure functions so they
// can be unit-tested. This module is bundled into sw.js only: do not import it
// from app code, or the bundler moves it into a shared chunk sw.js would import.

/** Precached app shell; the suffix is the build version. */
export const SHELL_CACHE_PREFIX = 'pulse-shell-';
/** Recently loaded API responses for offline reading. Holds private data. */
export const API_CACHE = 'pulse-api-v1';
/** Upper bound of stored API responses (article pages, feeds, folders). */
export const API_CACHE_MAX_ENTRIES = 60;

/**
 * - `api`: network first, falling back to the last stored response offline.
 * - `asset`: content-hashed build output, cache first.
 * - `navigation`: network first, falling back to the precached index.html.
 * - `static`: other precached files (favicon, icons), network first.
 * - `network`: not handled by the service worker at all.
 */
export type Strategy = 'api' | 'asset' | 'navigation' | 'static' | 'network';

export type RequestInfo = { method: string; url: string; mode: RequestMode };

// Read-only responses the reader needs to show previously loaded articles.
// Everything else under /api/ (sign-in, profile, administration, OPML, refresh)
// always goes to the network and is never stored.
function isOfflineReadablePath(path: string): boolean {
  return path === '/api/articles' || path.startsWith('/api/articles/') || path === '/api/feeds' || path === '/api/folders';
}

export function requestStrategy(request: RequestInfo, origin: string): Strategy {
  if (request.method !== 'GET') return 'network';
  const url = new URL(request.url);
  if (url.origin !== origin) return 'network';
  const path = url.pathname;
  if (path === '/api' || path.startsWith('/api/')) return isOfflineReadablePath(path) ? 'api' : 'network';
  if (path === '/sw.js') return 'network';
  if (request.mode === 'navigate') return 'navigation';
  if (path.startsWith('/assets/')) return 'asset';
  return 'static';
}

/**
 * Reports whether stored API responses must be dropped: on sign-in and
 * sign-out (the user may change), and whenever the server rejects the session.
 * `status` is null for a request whose response is not known yet.
 */
export function clearsApiCache(method: string, path: string, status: number | null): boolean {
  if (status === 401) return true;
  return method === 'POST' && (path === '/api/auth/login' || path === '/api/auth/logout');
}

export function isCacheableApiResponse(response: { status: number; type: ResponseType; headers: Headers }): boolean {
  if (response.status !== 200 || response.type !== 'basic') return false;
  if (/(^|,)\s*no-store\s*(,|$)/i.test(response.headers.get('cache-control') || '')) return false;
  return (response.headers.get('content-type') || '').toLowerCase().startsWith('application/json');
}

/** Oldest keys to delete so that at most API_CACHE_MAX_ENTRIES remain (keys in insertion order). */
export function entriesToEvict<T>(keys: readonly T[]): T[] {
  return keys.slice(0, Math.max(0, keys.length - API_CACHE_MAX_ENTRIES));
}

export function isObsoleteCache(name: string, currentShellCache: string): boolean {
  return name.startsWith(SHELL_CACHE_PREFIX) && name !== currentShellCache;
}
