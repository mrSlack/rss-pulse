import { useSyncExternalStore } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { applyUpdate, subscribeToUpdates, updateAvailable } from './pwa';
import { showsStaleData, subscribeToStaleData } from './staleData';

function subscribeToConnectivity(listener: () => void): () => void {
  window.addEventListener('online', listener);
  window.addEventListener('offline', listener);
  return () => {
    window.removeEventListener('online', listener);
    window.removeEventListener('offline', listener);
  };
}

/** Reports whether the browser has a network connection. */
export function useOnline(): boolean {
  return useSyncExternalStore(subscribeToConnectivity, () => navigator.onLine, () => true);
}

/**
 * The offline indicator (also shown while online when the reader shows
 * articles the service worker answered from its cache because the server did
 * not respond in time) and the "new version available" prompt.
 */
export function AppStatus({ signedIn }: { signedIn: boolean }) {
  const { t } = useTranslation();
  const online = useOnline();
  const update = useSyncExternalStore(subscribeToUpdates, updateAvailable, () => false);
  const stale = useSyncExternalStore(subscribeToStaleData, showsStaleData, () => false) && signedIn && online;
  if (online && !stale && !update) return null;

  return createPortal(
    <div className="app-status" role="status">
      {!online && <p className="app-status-item app-status-offline">
        <span className="app-status-dot" aria-hidden="true" />
        {signedIn ? t('offline.reader') : t('offline.signedOut')}
      </p>}
      {stale && <p className="app-status-item app-status-offline">
        <span className="app-status-dot" aria-hidden="true" />
        {t('offline.stale')}
      </p>}
      {update && <p className="app-status-item">
        {t('update.available')}
        <button className="app-status-action" type="button" onClick={applyUpdate}>{t('update.reload')}</button>
      </p>}
    </div>,
    document.body,
  );
}
