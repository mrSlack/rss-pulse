// Registers the service worker (production builds only) and tracks whether a
// newer version is waiting. A new version is not activated behind the user's
// back: the open page keeps using the files it was loaded with until the user
// chooses to reload, so lazily loaded chunks of the old version stay available.

const UPDATE_CHECK_INTERVAL_MS = 60 * 60 * 1000;

type Listener = () => void;
const listeners = new Set<Listener>();
let waitingWorker: ServiceWorker | null = null;

function setWaiting(worker: ServiceWorker | null) {
  waitingWorker = worker;
  listeners.forEach((listener) => listener());
}

function trackRegistration(registration: ServiceWorkerRegistration) {
  // Only an update is announced; the first installation has nothing to replace.
  if (registration.waiting && navigator.serviceWorker.controller) setWaiting(registration.waiting);
  registration.addEventListener('updatefound', () => {
    const installing = registration.installing;
    installing?.addEventListener('statechange', () => {
      if (installing.state === 'installed' && navigator.serviceWorker.controller) setWaiting(installing);
    });
  });
  // Long-lived tabs look for a new deployment now and then.
  window.setInterval(() => void registration.update().catch(() => undefined), UPDATE_CHECK_INTERVAL_MS);
}

export function registerServiceWorker(): void {
  if (!('serviceWorker' in navigator)) return;
  const register = () => {
    navigator.serviceWorker.register('/sw.js').then(trackRegistration).catch(() => {
      // Pulse works without offline support (for example over plain HTTP).
    });
  };
  if (document.readyState === 'complete') register();
  else window.addEventListener('load', register, { once: true });
}

export function subscribeToUpdates(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function updateAvailable(): boolean {
  return waitingWorker !== null;
}

/** Activates the waiting version and reloads the page once it controls it. */
export function applyUpdate(): void {
  const worker = waitingWorker;
  if (!worker) return;
  navigator.serviceWorker.addEventListener('controllerchange', () => window.location.reload(), { once: true });
  worker.postMessage({ type: 'skip-waiting' });
}
