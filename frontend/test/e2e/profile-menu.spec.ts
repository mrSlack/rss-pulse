import { expect, test } from '@playwright/test';

test('mobile profile menu stays above the sidebar without creating a horizontal scrollbar', async ({ page }) => {
  await page.setViewportSize({ width: 324, height: 349 });
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const body = path.endsWith('/auth/me')
      ? { id: 1, email: 'admin@example.com', role: 'Administrator', refreshIntervalMinutes: null }
      : path.endsWith('/articles')
        ? { items: [], hasMore: false }
        : path.endsWith('/refresh')
          ? { running: false }
          : [];
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Open navigation' }).click();
  const sidebar = page.locator('aside.sidebar.mobile-open');
  await sidebar.locator('.profile-menu-sidebar > summary').click();

  const dropdown = sidebar.locator('.profile-dropdown');
  await expect(dropdown).toBeVisible();
  await expect(dropdown.getByRole('button', { name: 'Administration' })).toBeVisible();
  expect(await dropdown.evaluate((element) => getComputedStyle(element).zIndex)).toBe('1000');
  const [sidebarLayer, topbarLayer] = await Promise.all([
    sidebar.evaluate((element) => Number(getComputedStyle(element).zIndex)),
    page.locator('.topbar').evaluate((element) => Number(getComputedStyle(element).zIndex)),
  ]);
  expect(sidebarLayer).toBeGreaterThan(topbarLayer);
  const adminItem = dropdown.getByRole('button', { name: 'Administration' });
  const itemBox = await adminItem.boundingBox();
  expect(itemBox).not.toBeNull();
  const isTopmost = await page.evaluate(({ x, y }) => {
    const topmost = document.elementFromPoint(x, y);
    return Boolean(topmost && document.querySelector('.profile-dropdown')?.contains(topmost));
  }, { x: itemBox!.x + itemBox!.width / 2, y: itemBox!.y + itemBox!.height / 2 });
  expect(isTopmost).toBe(true);

  const dimensions = await page.evaluate(() => ({
    pageClientWidth: document.documentElement.clientWidth,
    pageScrollWidth: document.documentElement.scrollWidth,
    sidebarClientWidth: document.querySelector('aside.sidebar')?.clientWidth ?? 0,
    sidebarScrollWidth: document.querySelector('aside.sidebar')?.scrollWidth ?? 0,
    overflowingElements: Array.from(document.querySelectorAll<HTMLElement>('*'))
      .map((element) => ({ element, rect: element.getBoundingClientRect() }))
      .filter(({ element, rect }) => getComputedStyle(element).position !== 'fixed' && (rect.left < 0 || rect.right > document.documentElement.clientWidth))
      .map(({ element, rect }) => ({ tag: element.tagName, className: element.className, left: rect.left, right: rect.right, width: rect.width }))
      .slice(0, 12),
  }));
  expect(dimensions.pageScrollWidth, JSON.stringify(dimensions)).toBeLessThanOrEqual(dimensions.pageClientWidth);
  expect(dimensions.sidebarScrollWidth, JSON.stringify(dimensions)).toBeLessThanOrEqual(dimensions.sidebarClientWidth);

  const [sidebarBox, dropdownBox] = await Promise.all([sidebar.boundingBox(), dropdown.boundingBox()]);
  expect(sidebarBox).not.toBeNull();
  expect(dropdownBox).not.toBeNull();
  expect(dropdownBox!.x).toBeGreaterThanOrEqual(sidebarBox!.x);
  expect(dropdownBox!.x + dropdownBox!.width).toBeLessThanOrEqual(sidebarBox!.x + sidebarBox!.width);
});

test('a reader feed request can be rejected or approved by an administrator', async ({ page }) => {
  let currentUser = { id: 2, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null };
  let nextRequestID = 1;
  const pendingRequests: Array<{ id: number; url: string; requestedBy: string; folderId: null; folderName: null; title: string; createdAt: string }> = [];
  const feeds: Array<{ id: number; title: string; url: string; folderId: null; folderName: null; unreadCount: number; lastCheckedAt: null; lastError: null }> = [];
  const submittedURLs: string[] = [];
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (path.endsWith('/auth/me')) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(currentUser) });
      return;
    }
    if (path.endsWith('/auth/login') && method === 'POST') {
      const credentials = route.request().postDataJSON() as { email: string };
      currentUser = credentials.email === 'admin@example.com'
        ? { id: 1, email: 'admin@example.com', role: 'Administrator', refreshIntervalMinutes: null }
        : { id: 2, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null };
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(currentUser) });
      return;
    }
    if (path.endsWith('/auth/logout') && method === 'POST') {
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === '/api/feeds' && method === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(feeds) });
      return;
    }
    if (path === '/api/feeds' && method === 'POST') {
      const payload = route.request().postDataJSON() as { url: string };
      submittedURLs.push(payload.url);
      pendingRequests.push({ id: nextRequestID++, url: payload.url, requestedBy: currentUser.email, folderId: null, folderName: null, title: '', createdAt: '2026-10-26T10:00:00Z' });
      await route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ requestId: pendingRequests.at(-1)!.id, status: 'pending' }) });
      return;
    }
    if (path.endsWith('/admin/settings')) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ refreshIntervalMinutes: 15, sharedFeedTTLMinutes: 15, users: [] }) });
      return;
    }
    if (path.endsWith('/admin/feed-requests') && method === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(pendingRequests) });
      return;
    }
    const approval = path.match(/\/api\/admin\/feed-requests\/(\d+)\/approve$/);
    if (approval && method === 'POST') {
      const requestIndex = pendingRequests.findIndex((request) => request.id === Number(approval[1]));
      const [request] = pendingRequests.splice(requestIndex, 1);
      feeds.push({ id: feeds.length + 1, title: 'Approved feed', url: request.url, folderId: null, folderName: null, unreadCount: 0, lastCheckedAt: null, lastError: null });
      await route.fulfill({ status: 204 });
      return;
    }
    const rejection = path.match(/\/api\/admin\/feed-requests\/(\d+)$/);
    if (rejection && method === 'DELETE') {
      const requestIndex = pendingRequests.findIndex((request) => request.id === Number(rejection[1]));
      if (requestIndex >= 0) pendingRequests.splice(requestIndex, 1);
      await route.fulfill({ status: 204 });
      return;
    }
    const body = path.endsWith('/articles')
      ? { items: [], hasMore: false }
      : path.endsWith('/refresh')
        ? { running: false }
        : [];
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });

  async function submitFeed(url: string) {
    await page.getByRole('button', { name: 'Add feed' }).click();
    await page.getByLabel('RSS feed URL').fill(url);
    await page.getByRole('button', { name: 'Subscribe' }).click();
    await expect(page.getByRole('status')).toContainText('The server will not fetch this feed until an administrator approves it.');
    await page.getByRole('button', { name: 'Close' }).click();
  }

  async function signOut() {
    await page.locator('.profile-menu-sidebar > summary').click();
    await page.getByRole('button', { name: 'Sign out' }).click();
    await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
  }

  async function signIn(email: string) {
    await page.getByLabel('Email').fill(email);
    await page.getByLabel('Password').fill('a-long-test-password');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.locator('.profile-menu-sidebar > summary')).toBeVisible();
  }

  async function openAdminQueue() {
    await page.locator('.profile-menu-sidebar > summary').click();
    await page.getByRole('button', { name: 'Administration' }).click();
    await expect(page.getByRole('heading', { name: 'Feed requests' })).toBeVisible();
  }

  await page.goto('/');
  await submitFeed('https://reject.example/rss');
  expect(pendingRequests).toHaveLength(1);
  await signOut();
  await signIn('admin@example.com');
  await openAdminQueue();
  await expect(page.getByText('https://reject.example/rss')).toBeVisible();
  await expect(page.locator('.admin-table tbody tr').filter({ hasText: 'https://reject.example/rss' })).toContainText('reader@example.com');
  await page.getByRole('button', { name: 'Reject' }).click();
  await expect(page.getByRole('status')).toContainText('Feed request rejected.');
  await expect(page.getByText('https://reject.example/rss')).toHaveCount(0);
  expect(pendingRequests).toHaveLength(0);

  await signOut();
  await signIn('reader@example.com');
  await submitFeed('https://approve.example/rss');
  expect(pendingRequests).toHaveLength(1);
  await signOut();
  await signIn('admin@example.com');
  await openAdminQueue();
  await expect(page.getByText('https://approve.example/rss')).toBeVisible();
  await expect(page.locator('.admin-table tbody tr').filter({ hasText: 'https://approve.example/rss' })).toContainText('reader@example.com');
  await page.getByRole('button', { name: 'Approve' }).click();
  await expect(page.getByRole('status')).toContainText('Feed approved. It will be fetched after approval.');
  await expect(page.getByText('https://approve.example/rss')).toHaveCount(0);
  expect(pendingRequests).toHaveLength(0);

  await signOut();
  await signIn('reader@example.com');
  await expect(page.locator('.tree-feed-row .feed-name')).toHaveText('Approved feed');
  await expect(page.locator('.tree-feed-row .feed-name')).not.toContainText('reject.example');
  expect(submittedURLs).toEqual(['https://reject.example/rss', 'https://approve.example/rss']);
});
