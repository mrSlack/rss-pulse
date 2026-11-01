import { useCallback, useEffect, useState } from 'react';
import { api, isAbortError, setUnauthorizedHandler, type SessionUser } from '../api';

/**
 * The signed-in user. A 401 from any authenticated request ends the session
 * and flags it as expired so the sign-in screen can say why it appeared.
 */
export function useSession() {
  const [user, setUser] = useState<SessionUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [signingIn, setSigningIn] = useState(false);
  const [expired, setExpired] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    api.currentUser(controller.signal)
      .then((current) => {
        if (controller.signal.aborted) return;
        setUser(current);
        setLoading(false);
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted || isAbortError(reason)) return;
        setUser(null);
        setLoading(false);
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      setExpired(true);
      setUser(null);
    });
    return () => setUnauthorizedHandler(null);
  }, []);

  const signIn = useCallback(async (email: string, password: string) => {
    setSigningIn(true);
    try {
      const signedIn = await api.login(email, password);
      setExpired(false);
      setUser(signedIn);
    } finally {
      setSigningIn(false);
    }
  }, []);

  const signOut = useCallback(async () => {
    await api.logout();
    setUser(null);
  }, []);

  /** Forgets a session that the server has already ended. */
  const clear = useCallback(() => setUser(null), []);

  return { user, loading, signingIn, expired, signIn, signOut, clear, setUser };
}
