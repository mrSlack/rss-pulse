# Pulse frontend

React 19, TypeScript and Vite. Setup, test commands and deployment are in the
repository [README](../README.md); architecture and behaviour are described in
[`docs/PROJECT.md`](../docs/PROJECT.md).

## Bundles

`npm run build` writes `dist/`. Rarely used parts are separate chunks loaded on
first use (`React.lazy`): the administration workspace, the profile, OPML
import, folder management and keyboard-shortcut dialogs, and the German and
Russian translation catalogs (English is bundled as the fallback). Third-party
code is in its own `vendor` chunk (see `vite.config.ts`), so an app-only
deployment leaves it cached. Everything under `dist/assets/` has a content hash
and is served as immutable.

## Offline reading (PWA)

- `public/manifest.webmanifest` and the icons in `public/` make Pulse
  installable. The PNG icons are rendered from `public/favicon.svg` and
  `icons/icon-maskable.svg` (for example with
  `rsvg-convert -w 512 -h 512 icons/icon-maskable.svg -o public/icon-maskable-512.png`).
- `src/sw/sw.ts` is the service worker. `vite-plugin-service-worker.ts` builds
  it into `dist/sw.js` (fixed name, no imports) and injects the precache list
  (all build output and `public/` files) and a content-derived version. Its
  caching rules are the pure functions in `src/sw/policy.ts`
  (`test/src/swPolicy.test.ts`). App code must not import from `src/sw/`.
- `src/pwa.ts` registers the worker in production builds only; the Vite dev
  server has no service worker. A new version waits until the user chooses
  "Reload" in the prompt (`src/AppStatus.tsx`).
- `src/offlineData.ts` remembers the signed-in account for offline starts and
  deletes it together with the API cache on sign-out, on a 401, and when
  another account signs in.

Service workers need a secure context: HTTPS, or `http://localhost`. After
changing `src/sw/`, check `test/e2e/pwa.spec.ts`; Playwright runs it against a
production build served by `vite preview` on port 4173, started by
`playwright.config.ts` next to the dev server.

To reset a browser during development, unregister the worker and clear site
data in the browser's developer tools (Application panel).
