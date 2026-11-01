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
 * How long a cacheable API request waits for the network before the stored
 * copy is shown instead. A connection that hangs (captive portal, dead mobile
 * link) would otherwise never fall back to it. Without a stored copy the
 * request keeps waiting; the page's own request timeout (src/api.ts) ends it.
 *
 * A server that is merely slow also trips this limit, so a stored copy is
 * never passed off as fresh: it carries STALE_HEADER (the page then says that
 * it shows saved articles), the worker tells the page when the network answer
 * arrives (lateAnswerMessage), and for SLOW_SERVER_WINDOW_MS after such a
 * late answer requests wait for the network instead (fallbackTimeout).
 */
export const API_NETWORK_TIMEOUT_MS = 4000;
/**
 * After a network answer arrived late (the server is reachable but slow),
 * cacheable requests wait this long for the network without the time limit,
 * so the page's reload gets fresh data rather than the stored copy again.
 */
export const SLOW_SERVER_WINDOW_MS = 2 * 60 * 1000;
/** Header the worker adds to a stored copy it answers with (value `stale`); read by src/staleData.ts. */
export const STALE_HEADER = 'X-Pulse-Cache';
/** Messages to the page; the same strings are in src/staleData.ts. */
export type LateAnswerMessage = { type: 'pulse-api-fresh' } | { type: 'pulse-session-expired' };

/**
 * - `api`: network first, falling back to the last stored response (marked
 *   stale) when the network fails or takes longer than API_NETWORK_TIMEOUT_MS.
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

/**
 * The time limit after which a cacheable request is answered from the cache,
 * or null to wait for the network (until it fails) because a network answer
 * arrived late less than SLOW_SERVER_WINDOW_MS ago (`lateAnswerAt`).
 */
export function fallbackTimeout(lateAnswerAt: number | null, now: number): number | null {
  return lateAnswerAt !== null && now - lateAnswerAt < SLOW_SERVER_WINDOW_MS ? null : API_NETWORK_TIMEOUT_MS;
}

/** A copy of a stored response marked with STALE_HEADER. */
export function markedStale(response: Response): Response {
  const headers = new Headers(response.headers);
  headers.set(STALE_HEADER, 'stale');
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
}

/**
 * What to tell the page when the network answers a request that was already
 * answered from the cache: fresh data to load, or a session that ended (the
 * page never saw the 401 itself). Null when nothing changes.
 */
export function lateAnswerMessage(status: number): LateAnswerMessage | null {
  if (status === 401) return { type: 'pulse-session-expired' };
  if (status >= 200 && status < 300) return { type: 'pulse-api-fresh' };
  return null;
}

/**
 * Settles with the network response, or with the stored copy (from `cached`,
 * undefined when there is none) when the network fails or has not answered
 * within `timeoutMs` (null: no time limit). Without a stored copy it waits
 * for the network however long it takes. The network request is not
 * cancelled: the caller may still use its response to refresh the cache. An
 * aborted request (`aborted()`) is never answered from the cache.
 */
export function networkFirstWithTimeout<T>(
  network: Promise<T>,
  cached: () => Promise<T | undefined>,
  { timeoutMs, aborted }: { timeoutMs: number | null; aborted: () => boolean },
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    let settled = false;
    const settle = (finish: () => void) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      finish();
    };
    const lookup = () => cached().catch(() => undefined);
    const timer = timeoutMs === null ? undefined : setTimeout(() => {
      void lookup().then((copy) => {
        if (copy !== undefined) settle(() => resolve(copy));
      });
    }, timeoutMs);
    network.then(
      (response) => settle(() => resolve(response)),
      (reason: unknown) => {
        if (settled) return;
        if (aborted()) {
          settle(() => reject(reason));
          return;
        }
        void lookup().then((copy) => settle(() => (copy !== undefined ? resolve(copy) : reject(reason))));
      },
    );
  });
}

/** Oldest keys to delete so that at most API_CACHE_MAX_ENTRIES remain (keys in insertion order). */
export function entriesToEvict<T>(keys: readonly T[]): T[] {
  return keys.slice(0, Math.max(0, keys.length - API_CACHE_MAX_ENTRIES));
}

export function isObsoleteCache(name: string, currentShellCache: string): boolean {
  return name.startsWith(SHELL_CACHE_PREFIX) && name !== currentShellCache;
}
