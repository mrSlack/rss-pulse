import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, isAbortError, setUnauthorizedHandler, type SessionUser } from '../api';
import { clearOfflineData, forgetRememberChoice, prepareOfflineData, rememberedUser, rememberUser, setRemembersDevice } from '../offlineData';

/**
 * The signed-in user. A 401 from any authenticated request ends the session
 * and flags it as expired so the sign-in screen can say why it appeared.
 *
 * Without a network connection the account last signed in on this device is
 * used (if the user chose to remember this device), so articles stored for
 * offline reading can be shown; the server checks the session again with the
 * first request that reaches it. Offline data is dropped whenever the session
 * ends or another account signs in, and at start when the device is not
 * remembered (see offlineData.ts).
 */
export function useSession() {
  const [user, setUser] = useState<SessionUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [signingIn, setSigningIn] = useState(false);
  const [expired, setExpired] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    prepareOfflineData()
      .then(() => api.currentUser(controller.signal))
      .then((current) => {
        if (controller.signal.aborted) return;
        void rememberUser(current);
        setUser(current);
        setLoading(false);
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted || isAbortError(reason)) return;
        const offline = reason instanceof ApiError && reason.status === 0;
        if (reason instanceof ApiError && reason.status === 401) void clearOfflineData();
        setUser(offline ? rememberedUser() : null);
        setLoading(false);
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      void clearOfflineData();
      setExpired(true);
      setUser(null);
    });
    return () => setUnauthorizedHandler(null);
  }, []);

  /** Signs in; `remember` is the "Remember me on this device for offline use" choice. */
  const signIn = useCallback(async (email: string, password: string, remember: boolean) => {
    setSigningIn(true);
    try {
      const signedIn = await api.login(email, password);
      await setRemembersDevice(remember);
      await rememberUser(signedIn);
      setExpired(false);
      setUser(signedIn);
    } finally {
      setSigningIn(false);
    }
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      // Even if the server could not be reached, this device forgets the data,
      // and the next person signing in here starts from the default choice.
      await clearOfflineData();
      forgetRememberChoice();
    }
    setUser(null);
  }, []);

  /** Forgets a session that the server has already ended. */
  const clear = useCallback(() => {
    void clearOfflineData();
    setUser(null);
  }, []);

  /** Replaces the signed-in user's details (for example after a profile change). */
  const updateUser = useCallback((updated: SessionUser) => {
    void rememberUser(updated);
    setUser(updated);
  }, []);

  return { user, loading, signingIn, expired, signIn, signOut, clear, setUser: updateUser };
}
