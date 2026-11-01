// Tracks whether the page shows API data the service worker answered from its
// cache, and passes on the worker's messages about such answers (see
// src/sw/policy.ts: the worker marks a stored copy with STALE_HEADER and
// reports when the network answer arrives later). The constants repeat the
// worker's, because the page must not import the worker's modules.

/** Response header of a stored copy; its value is `stale`. */
export const STALE_HEADER = 'X-Pulse-Cache';
/** The network answered after a stored copy was shown: load the data again. */
export const SW_MESSAGE_FRESH_DATA = 'pulse-api-fresh';
/** The network answered 401 after a stored copy was shown: the session ended. */
export const SW_MESSAGE_SESSION_EXPIRED = 'pulse-session-expired';

type Listener = () => void;
const listeners = new Set<Listener>();
let stale = false;

function setStale(value: boolean) {
  if (stale === value) return;
  stale = value;
  listeners.forEach((listener) => listener());
}

// The requests the worker may answer from its cache (isOfflineReadablePath).
function isOfflineReadable(method: string, path: string): boolean {
  return method === 'GET' && (path === '/articles' || path.startsWith('/articles/') || path === '/feeds' || path === '/folders');
}

/**
 * Records a successful API response. `path` is relative to /api, without the
 * query. A stored copy turns the notice on; a fresh answer to the same kind of
 * request turns it off.
 */
export function noteApiResponse(method: string, path: string, response: Response): void {
  if (!isOfflineReadable(method.toUpperCase(), path)) return;
  setStale(response.headers.get(STALE_HEADER) === 'stale');
}

/** Forgets the notice (for example when the session ends). */
export function resetStaleData(): void {
  setStale(false);
}

export function showsStaleData(): boolean {
  return stale;
}

export function subscribeToStaleData(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Calls `listener` for each message of `type` from the service worker. */
export function onServiceWorkerMessage(type: string, listener: () => void): () => void {
  const container = typeof navigator === 'undefined' ? undefined : (navigator.serviceWorker as ServiceWorkerContainer | undefined);
  if (!container) return () => undefined;
  const handle = (event: MessageEvent) => {
    const data: unknown = event.data;
    if (typeof data === 'object' && data !== null && (data as { type?: unknown }).type === type) listener();
  };
  container.addEventListener('message', handle);
  return () => container.removeEventListener('message', handle);
}
