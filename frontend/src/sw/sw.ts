// Pulse service worker. Built into dist/sw.js by the plugin in
// vite-plugin-service-worker.ts, which replaces the manifest placeholder with
// the build's file list and version. It must stay self-contained: import only
// ./policy (nothing the app imports), so the bundle has no imports.
import {
  API_CACHE,
  SHELL_CACHE_PREFIX,
  clearsApiCache,
  entriesToEvict,
  isCacheableApiResponse,
  isObsoleteCache,
  requestStrategy,
} from './policy';

type PrecacheManifest = { version: string; urls: string[] };

// Minimal service worker typings; the project compiles against the DOM library.
type ExtendableEvent = Event & { waitUntil(promise: Promise<unknown>): void };
type FetchEvent = ExtendableEvent & { request: Request; respondWith(response: Promise<Response>): void };
type ExtendableMessageEvent = ExtendableEvent & { data: unknown };
type ServiceWorkerScope = {
  location: Location;
  skipWaiting(): Promise<void>;
  clients: { claim(): Promise<void> };
  addEventListener(type: 'install' | 'activate', listener: (event: ExtendableEvent) => void): void;
  addEventListener(type: 'fetch', listener: (event: FetchEvent) => void): void;
  addEventListener(type: 'message', listener: (event: ExtendableMessageEvent) => void): void;
};

const worker = self as unknown as ServiceWorkerScope;
// A JSON string so the minifier leaves the placeholder alone.
const manifest = JSON.parse('__PULSE_PRECACHE_MANIFEST__') as PrecacheManifest;
const shellCache = `${SHELL_CACHE_PREFIX}${manifest.version}`;

// Incremented whenever the API cache is cleared. A response fetched before a
// clear must not be stored after it (it may belong to the previous user).
let apiGeneration = 0;

function clearApiCache(): Promise<boolean> {
  apiGeneration += 1;
  return caches.delete(API_CACHE);
}

worker.addEventListener('install', (event) => {
  // Bypass the HTTP cache so the precache matches this build exactly.
  event.waitUntil(caches.open(shellCache).then((cache) => cache.addAll(manifest.urls.map((url) => new Request(url, { cache: 'reload' })))));
  // The new version waits until the page asks for it (see src/pwa.ts), so an
  // open page keeps the files of the version it was loaded with.
});

worker.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const names = await caches.keys();
    await Promise.all(names.filter((name) => isObsoleteCache(name, shellCache)).map((name) => caches.delete(name)));
    await worker.clients.claim();
  })());
});

worker.addEventListener('message', (event) => {
  const type = typeof event.data === 'object' && event.data !== null ? (event.data as { type?: unknown }).type : undefined;
  if (type === 'skip-waiting') void worker.skipWaiting();
  if (type === 'clear-api-cache') event.waitUntil(clearApiCache());
});

async function storeApiResponse(request: Request, response: Response, generation: number) {
  const cache = await caches.open(API_CACHE);
  if (generation !== apiGeneration) return;
  // Delete first so a refreshed entry moves to the end of the insertion order.
  await cache.delete(request, { ignoreVary: true });
  await cache.put(request, response);
  if (generation !== apiGeneration) {
    await caches.delete(API_CACHE);
    return;
  }
  const evicted = entriesToEvict(await cache.keys());
  await Promise.all(evicted.map((key) => cache.delete(key)));
}

async function networkFirstApi(event: FetchEvent): Promise<Response> {
  const { request } = event;
  const generation = apiGeneration;
  let response: Response;
  try {
    response = await fetch(request);
  } catch (reason) {
    if (request.signal.aborted) throw reason;
    const cached = await caches.match(request, { cacheName: API_CACHE, ignoreVary: true });
    if (cached) return cached;
    throw reason;
  }
  if (clearsApiCache(request.method, new URL(request.url).pathname, response.status)) {
    event.waitUntil(clearApiCache());
  } else if (isCacheableApiResponse(response)) {
    event.waitUntil(storeApiResponse(request, response.clone(), generation).catch(() => undefined));
  }
  return response;
}

async function cacheFirst(request: Request): Promise<Response> {
  const cached = await caches.match(request);
  if (cached) return cached;
  const response = await fetch(request);
  if (response.ok && response.type === 'basic') {
    const copy = response.clone();
    void caches.open(shellCache).then((cache) => cache.put(request, copy)).catch(() => undefined);
  }
  return response;
}

async function networkFirst(request: Request, fallbackUrl: string): Promise<Response> {
  try {
    return await fetch(request);
  } catch (reason) {
    const cached = await caches.match(fallbackUrl, { cacheName: shellCache });
    if (cached) return cached;
    throw reason;
  }
}

worker.addEventListener('fetch', (event) => {
  const { request } = event;
  const url = new URL(request.url);
  if (url.origin === worker.location.origin && clearsApiCache(request.method, url.pathname, null)) {
    // Sign-in and sign-out pass through untouched; only the stored data goes.
    event.waitUntil(clearApiCache());
    return;
  }
  switch (requestStrategy(request, worker.location.origin)) {
    case 'api':
      event.respondWith(networkFirstApi(event));
      return;
    case 'asset':
      event.respondWith(cacheFirst(request));
      return;
    case 'navigation':
      event.respondWith(networkFirst(request, '/index.html'));
      return;
    case 'static':
      event.respondWith(networkFirst(request, url.pathname));
      return;
    default:
      // Not handled: the browser fetches it as if there were no service worker.
  }
});
