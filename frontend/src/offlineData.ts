import type { SessionUser, UserRole } from './api';

// Data kept on this device so a signed-in reader can open Pulse offline: the
// account last seen by this browser (localStorage) and the API responses the
// service worker stored. Both belong to one user and are dropped on sign-out,
// when the server rejects the session, and when another account signs in.

/** Prefix of the service worker's API caches (see src/sw/policy.ts). */
export const API_CACHE_PREFIX = 'pulse-api';
const USER_KEY = 'pulse.offlineUser';

const roles: readonly UserRole[] = ['Administrator', 'Reader'];

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

/** Records the signed-in account; another account's offline data is dropped first. */
export function rememberUser(user: SessionUser): Promise<void> {
  const previous = rememberedUser();
  const cleared = previous !== null && previous.id !== user.id ? clearOfflineData() : Promise.resolve();
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
