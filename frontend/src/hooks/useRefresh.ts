import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, errorMessage, isAbortError } from '../api';

const pollIntervalMs = 750;
const maxWaitMs = 180000;

type Options = {
  onError: (message: string) => void;
  /** Called once the refresh finished (or is still running after the wait). */
  onFinished: () => Promise<void> | void;
};

/**
 * Requests a feed refresh (or follows one the server started, such as after a
 * feed URL change) and polls its status until it finishes.
 */
export function useRefresh({ onError, onFinished }: Options) {
  const { t } = useTranslation();
  const [refreshing, setRefreshing] = useState(false);
  const controllerRef = useRef<AbortController | null>(null);

  useEffect(() => () => controllerRef.current?.abort(), []);

  const runRefresh = useCallback(async (request = true) => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setRefreshing(true);
    try {
      if (request) await api.refresh();
      const startedAt = Date.now();
      let stillRunning = true;
      while (stillRunning && Date.now() - startedAt < maxWaitMs) {
        await new Promise((resolve) => window.setTimeout(resolve, pollIntervalMs));
        if (controller.signal.aborted) return;
        stillRunning = (await api.refreshStatus(controller.signal)).running;
      }
      await onFinished();
      if (stillRunning) onError(t('errors.refreshBackground'));
    } catch (reason) {
      if (!isAbortError(reason)) onError(errorMessage(reason, t('errors.refresh')));
    } finally {
      if (controllerRef.current === controller) {
        controllerRef.current = null;
        setRefreshing(false);
      }
    }
  }, [onError, onFinished, t]);

  return { refreshing, runRefresh };
}
