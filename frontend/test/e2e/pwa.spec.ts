import { expect, test, type BrowserContext, type Page } from '@playwright/test';

// Runs against the production preview server from playwright.config.ts: only
// production builds contain and register the service worker.
test.use({ baseURL: 'http://127.0.0.1:4173', serviceWorkers: 'allow' });

const reader = { id: 7, email: 'offline@example.com', role: 'Reader', refreshIntervalMinutes: null };
const feeds = [{
  id: 1, title: 'The Daily Signal', url: 'https://example.com/feed.xml', folderId: null, folderName: null,
  unreadCount: 2, lastCheckedAt: '2026-04-15T11:55:00Z', lastError: null,
}];
const articles = [1, 2].map((id) => ({
  id, feedId: 1, feedTitle: 'The Daily Signal', feedUrl: 'https://example.com/feed.xml',
  title: `Stored headline ${id}`, url: `https://example.com/articles/${id}`,
  summary: `Summary of stored article ${id}.`, publishedAt: '2026-04-15T11:00:00Z', isRead: false, isSaved: false,
}));

/**
 * Mocks the API on the browser context, which (in Chromium) also sees the
 * requests the service worker makes. Returns the list of requested API paths.
 */
async function mockApi(context: BrowserContext) {
  const requested: string[] = [];
  await context.route('**/api/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    requested.push(`${request.method()} ${path}`);
    let body: unknown = [];
    let status = 200;
    if (path === '/api/auth/me') body = reader;
    else if (path === '/api/auth/logout') status = 204;
    else if (path === '/api/feeds') body = feeds;
    else if (path === '/api/folders') body = [];
    else if (path === '/api/articles') body = { items: articles, hasMore: false, nextCursor: null, latestArticleId: 2 };
    else if (path === '/api/refresh') body = { running: false };
    if (status === 204) await route.fulfill({ status });
    else await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
  });
  return requested;
}

async function goOffline(context: BrowserContext) {
  await context.unrouteAll({ behavior: 'wait' });
  // Every request now fails as without a network, including the service
  // worker's own; setOffline also makes navigator.onLine false.
  await context.route('**/*', (route) => route.abort('internetdisconnected'));
  await context.setOffline(true);
}

async function waitForServiceWorkerControl(page: Page) {
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready;
    if (!navigator.serviceWorker.controller) {
      await new Promise((resolve) => navigator.serviceWorker.addEventListener('controllerchange', resolve, { once: true }));
    }
  });
}

async function apiCacheNames(page: Page): Promise<string[]> {
  return page.evaluate(async () => (await caches.keys()).filter((name) => name.startsWith('pulse-api')));
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('pulse-theme', 'light');
    localStorage.setItem('pulse.language', 'en');
  });
});

test('the app is installable: manifest, icons and a registered service worker', async ({ page, context }) => {
  await mockApi(context);
  await page.goto('/');
  await expect(page.getByText('Stored headline 1')).toBeVisible();

  const manifestHref = await page.locator('link[rel="manifest"]').getAttribute('href');
  expect(manifestHref).toBe('/manifest.webmanifest');
  const manifest = await (await page.request.get(manifestHref!)).json() as { name: string; start_url: string; display: string; icons: Array<{ src: string; sizes: string; purpose: string }> };
  expect(manifest).toMatchObject({ name: 'Pulse', start_url: '/', display: 'standalone' });
  expect(manifest.icons.map((icon) => icon.sizes)).toEqual(expect.arrayContaining(['192x192', '512x512']));
  expect(manifest.icons.some((icon) => icon.purpose === 'maskable')).toBe(true);
  for (const icon of manifest.icons) {
    const response = await page.request.get(icon.src);
    expect(response.ok(), icon.src).toBe(true);
  }

  await waitForServiceWorkerControl(page);
  const scriptURL = await page.evaluate(async () => (await navigator.serviceWorker.ready).active?.scriptURL);
  expect(scriptURL).toBe('http://127.0.0.1:4173/sw.js');
});

test('a previously loaded article list stays readable offline', async ({ page, context }) => {
  const requested = await mockApi(context);
  await page.goto('/');
  await waitForServiceWorkerControl(page);
  // Load the list again under the service worker's control so it is stored.
  await page.reload();
  await expect(page.getByText('Stored headline 2')).toBeVisible();
  await expect.poll(() => apiCacheNames(page)).toEqual(['pulse-api-v1']);
  expect(requested).toContain('GET /api/articles');

  await goOffline(context);
  await page.reload();
  await expect(page.getByRole('status')).toHaveText('You are offline. Articles loaded earlier remain readable.');
  await expect(page.getByText('Stored headline 1')).toBeVisible();
  await page.getByText('Stored headline 1').click();
  await expect(page.getByText('Summary of stored article 1.').last()).toBeVisible();
});

test('signing out removes the stored articles and the remembered account', async ({ page, context }) => {
  await mockApi(context);
  await page.goto('/');
  await waitForServiceWorkerControl(page);
  await page.reload();
  await expect(page.getByText('Stored headline 1')).toBeVisible();
  await expect.poll(() => apiCacheNames(page)).toEqual(['pulse-api-v1']);

  await page.locator('.profile-menu-sidebar > summary').click();
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
  expect(await apiCacheNames(page)).toEqual([]);
  expect(await page.evaluate(() => localStorage.getItem('pulse.offlineUser'))).toBeNull();

  // Offline, nobody is signed in on this device any more.
  await goOffline(context);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
  await expect(page.getByText('Stored headline 1')).toHaveCount(0);
});
