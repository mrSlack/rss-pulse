import type { SessionUser, UserRole } from './api';

// Data kept on this device so a signed-in reader can open Pulse offline: the
// account last seen by this browser (localStorage) and the API responses the
// service worker stored. Both belong to one user and are dropped on sign-out,
// when the server rejects the session, and when another account signs in.
//
// The user chooses whether this device is remembered ("Remember me on this
// device for offline use"). When it is not, the account is never stored and
// API responses stored during a visit are deleted at the next start (before
// the session check), on sign-out, and when the option is turned off.

/** Prefix of the service worker's API caches (see src/sw/policy.ts). */
export const API_CACHE_PREFIX = 'pulse-api';
const USER_KEY = 'pulse.offlineUser';
/** The user's choice, 'true' or 'false'; missing means the default. */
const REMEMBER_KEY = 'pulse.offlineRemember';

const roles: readonly UserRole[] = ['Administrator', 'Reader'];

/** Reports whether Pulse runs as an installed app rather than in a browser tab. */
function runsInstalled(): boolean {
  try {
    if (['standalone', 'window-controls-overlay'].some((mode) => window.matchMedia(`(display-mode: ${mode})`).matches)) return true;
  } catch {
    // No matchMedia (old browsers, tests): treat as a browser tab.
  }
  return (navigator as Navigator & { standalone?: boolean }).standalone === true;
}

/**
 * Whether this device keeps the account and loaded articles for offline use.
 * Without an explicit choice it is on in the installed app, which people
 * install on their own devices to use offline, and off in a browser tab,
 * which may be on a shared or public computer.
 */
export function remembersDevice(): boolean {
  try {
    const choice = window.localStorage.getItem(REMEMBER_KEY);
    if (choice === 'true') return true;
    if (choice === 'false') return false;
  } catch {
    // Storage is unavailable: nothing can be remembered anyway.
    return false;
  }
  return runsInstalled();
}

/** Records the choice. Turning it off deletes the remembered account and stored API responses at once. */
export async function setRemembersDevice(remember: boolean): Promise<void> {
  try {
    window.localStorage.setItem(REMEMBER_KEY, String(remember));
  } catch {
    // Without storage nothing is remembered.
  }
  if (!remember) await clearOfflineData();
}

/** Forgets the choice (on sign-out), so the next sign-in on this browser starts from the default. */
export function forgetRememberChoice(): void {
  try {
    window.localStorage.removeItem(REMEMBER_KEY);
  } catch {
    // Storage is unavailable; nothing was stored.
  }
}

/**
 * Prepares the start of a visit: when this device is not remembered, data of
 * an earlier visit (an account stored before the choice existed, API
 * responses) is deleted before the first request.
 */
export function prepareOfflineData(): Promise<void> {
  return remembersDevice() ? Promise.resolve() : clearOfflineData();
}

/** The account to show when Pulse starts without a network connection. */
export function rememberedUser(): SessionUser | null {
  try {
    const value: unknown = JSON.parse(window.localStorage.getItem(USER_KEY) || 'null');
    if (value === null || typeof value !== 'object') return null;
    const user = value as Record<string, unknown>;
    if (typeof user.id !== 'number' || typeof user.email !== 'string' || !roles.includes(user.role as UserRole)) return null;
    return {
      id: user.id,
      email: user.email,
      role: user.role as UserRole,
      refreshIntervalMinutes: typeof user.refreshIntervalMinutes === 'number' ? user.refreshIntervalMinutes : null,
      ...(typeof user.firstName === 'string' ? { firstName: user.firstName } : {}),
      ...(typeof user.lastName === 'string' ? { lastName: user.lastName } : {}),
      ...(typeof user.nickname === 'string' ? { nickname: user.nickname } : {}),
    };
  } catch {
    return null;
  }
}

/**
 * Records the signed-in account if this device is remembered; another
 * account's offline data is dropped first.
 */
export function rememberUser(user: SessionUser): Promise<void> {
  const previous = rememberedUser();
  const cleared = previous !== null && previous.id !== user.id ? clearOfflineData() : Promise.resolve();
  if (!remembersDevice()) {
    try {
      window.localStorage.removeItem(USER_KEY);
    } catch {
      // Storage is unavailable; nothing was remembered.
    }
    return cleared;
  }
  try {
    const { id, email, role, refreshIntervalMinutes, firstName, lastName, nickname } = user;
    window.localStorage.setItem(USER_KEY, JSON.stringify({ id, email, role, refreshIntervalMinutes, firstName, lastName, nickname }));
  } catch {
    // Without storage Pulse simply cannot start offline.
  }
  return cleared;
}

/** Forgets the remembered account and every stored API response. */
export async function clearOfflineData(): Promise<void> {
  try {
    window.localStorage.removeItem(USER_KEY);
  } catch {
    // Storage is unavailable; nothing was remembered.
  }
  // Tell the service worker first so a response still in flight is not stored
  // after the cache has been deleted.
  try {
    navigator.serviceWorker?.controller?.postMessage({ type: 'clear-api-cache' });
  } catch {
    // No controlling service worker.
  }
  if (typeof caches === 'undefined') return;
  try {
    const names = await caches.keys();
    await Promise.all(names.filter((name) => name.startsWith(API_CACHE_PREFIX)).map((name) => caches.delete(name)));
  } catch {
    // Cache storage is unavailable (for example in a private window).
  }
}
