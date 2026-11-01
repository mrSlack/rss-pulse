import { languageReady } from './i18n';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { registerServiceWorker } from './pwa';
import './styles.css';

// Render once the saved interface language is loaded, so it does not flash English.
void languageReady.then(() => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
});

// Offline support needs the built sw.js; the dev server does not provide it.
if (import.meta.env.PROD) registerServiceWorker();
