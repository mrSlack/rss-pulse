import { expect, test } from '@playwright/test';

type TestUser = {
  id: number;
  email: string;
  role: 'Administrator' | 'Reader';
  refreshIntervalMinutes: number | null;
};

async function expectNoHorizontalOverflow(page: import('@playwright/test').Page) {
  const dimensions = await page.evaluate(() => {
    const root = document.documentElement;
    const containers = Array.from(document.querySelectorAll<HTMLElement>(
      '.admin-workspace, .admin-subnav, .admin-section-content, .admin-user-management, .admin-card',
    )).filter((element) => element.getClientRects().length > 0);
    const offenders = Array.from(document.querySelectorAll<HTMLElement>('body *')).filter((element) => {
      if (element.getClientRects().length === 0) return false;
      const rect = element.getBoundingClientRect();
      return rect.right > root.clientWidth + 1 || (element.scrollWidth > element.clientWidth + 1 && getComputedStyle(element).overflowX !== 'auto');
    }).map((element) => {
      const rect = element.getBoundingClientRect();
      return { tag: element.tagName, className: String(element.className), text: element.textContent?.trim().slice(0, 40), left: Math.round(rect.left), right: Math.round(rect.right), width: Math.round(rect.width), scrollWidth: element.scrollWidth, overflowX: getComputedStyle(element).overflowX };
    }).slice(0, 12);
    const outside = containers.map((element) => {
      const rect = element.getBoundingClientRect();
      return { name: element.className, left: Math.round(rect.left), right: Math.round(rect.right) };
    }).filter((rect) => rect.left < 0 || rect.right > root.clientWidth);
    return { clientWidth: root.clientWidth, scrollWidth: root.scrollWidth, outside, offenders };
  });
  expect(dimensions.scrollWidth, JSON.stringify(dimensions)).toBeLessThanOrEqual(dimensions.clientWidth);
  expect(dimensions.outside, JSON.stringify(dimensions)).toEqual([]);
}

test('admin workspace visits every section without API, script, or layout errors', async ({ page, request }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  const liveAPI = process.env.PLAYWRIGHT_API_URL || 'http://api:8080';
  const apiRouteResponse = await request.get(`${liveAPI}/api/admin/stop-words`);

  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  const httpErrors: string[] = [];
  const failedRequests: string[] = [];
  const unexpectedAPIRequests: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('response', (response) => {
    if (response.url().includes('/api/') && response.status() >= 400) {
      httpErrors.push(`${response.status()} ${response.request().method()} ${response.url()}`);
    }
  });
  page.on('requestfailed', (requestEvent) => {
    if (requestEvent.url().includes('/api/')) failedRequests.push(`${requestEvent.method()} ${requestEvent.url()}`);
  });

  let currentUser: TestUser = {
    id: 1,
    email: 'admin@example.com',
    role: 'Administrator',
    refreshIntervalMinutes: null,
  };
  let users: TestUser[] = [
    currentUser,
    { id: 2, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null },
  ];
  let feedRequests = [
    { id: 11, url: 'https://one.example/rss', requestedBy: 'reader@example.com', folderId: null, folderName: null, title: '', createdAt: '2026-10-26T10:00:00Z' },
    { id: 12, url: 'https://two.example/rss', requestedBy: 'reader@example.com', folderId: null, folderName: null, title: '', createdAt: '2026-10-26T10:01:00Z' },
  ];
  let stopWords = [{ id: 21, term: 'pornography', createdAt: '2026-10-26T10:00:00Z' }];
  let blockedArticles = [{
    id: 31,
    articleGuid: 'quarantine-31',
    summary: '<p>A matching article retained for administrator review.</p>',
    publishedAt: '2026-10-25T09:00:00Z',
    status: 'pending',
    canModerate: true,
    decidedAt: null,
    articleTitle: 'Blocked sample article',
    articleUrl: 'https://news.example/story',
    feedTitle: 'Sample feed',
    feedUrl: 'https://news.example/rss',
    addedBy: 'reader@example.com',
    matchedTerm: 'pornography',
    firstBlockedAt: '2026-10-26T10:00:00Z',
    lastBlockedAt: '2026-10-26T10:00:00Z',
  }];
  let refreshIntervalMinutes = 15;
  let sharedFeedTTLMinutes = 15;

  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

    if (path === '/api/auth/me' && method === 'GET') return json(currentUser);
    if (path === '/api/feeds' && method === 'GET') return json([]);
    if (path === '/api/folders' && method === 'GET') return json([]);
    if (path === '/api/articles' && method === 'GET') return json({ items: [], hasMore: false });
    if (path === '/api/refresh' && method === 'GET') return json({ running: false });

    if (path === '/api/admin/settings' && method === 'GET') {
      return json({ refreshIntervalMinutes, sharedFeedTTLMinutes, users });
    }
    if (path === '/api/admin/settings' && method === 'PUT') {
      const body = route.request().postDataJSON() as { refreshIntervalMinutes: number; sharedFeedTTLMinutes: number };
      refreshIntervalMinutes = body.refreshIntervalMinutes;
      sharedFeedTTLMinutes = body.sharedFeedTTLMinutes;
      return json({ refreshIntervalMinutes, sharedFeedTTLMinutes });
    }
    if (path === '/api/admin/feed-requests' && method === 'GET') return json(feedRequests);
    const approve = path.match(/^\/api\/admin\/feed-requests\/(\d+)\/approve$/);
    if (approve && method === 'POST') {
      feedRequests = feedRequests.filter((item) => item.id !== Number(approve[1]));
      return route.fulfill({ status: 204 });
    }
    const reject = path.match(/^\/api\/admin\/feed-requests\/(\d+)$/);
    if (reject && method === 'DELETE') {
      feedRequests = feedRequests.filter((item) => item.id !== Number(reject[1]));
      return route.fulfill({ status: 204 });
    }
    if (path === '/api/admin/stop-words' && method === 'GET') return json(stopWords);
    if (path === '/api/admin/stop-words' && method === 'POST') {
      const body = route.request().postDataJSON() as { term: string };
      const word = { id: 22, term: body.term.toLowerCase().trim(), createdAt: '2026-10-26T10:02:00Z' };
      stopWords = [...stopWords, word];
      return json(word, 201);
    }
    const removeWord = path.match(/^\/api\/admin\/stop-words\/(\d+)$/);
    if (removeWord && method === 'DELETE') {
      stopWords = stopWords.filter((word) => word.id !== Number(removeWord[1]));
      return route.fulfill({ status: 204 });
    }
    if (path === '/api/admin/blocked-articles' && method === 'GET') return json(blockedArticles);
    if (path === '/api/admin/blocked-articles/31/allow' && method === 'POST') {
      blockedArticles = [{ ...blockedArticles[0], id: 32, articleGuid: 'quarantine-32', articleTitle: 'Next pending article' }];
      return route.fulfill({ status: 204 });
    }
    if (path === '/api/admin/users' && method === 'POST') {
      const body = route.request().postDataJSON() as { email: string; role: TestUser['role'] };
      const user = { id: 3, email: body.email, role: body.role, refreshIntervalMinutes: null };
      users = [...users, user];
      return json(user, 201);
    }
    const updateUser = path.match(/^\/api\/admin\/users\/(\d+)$/);
    if (updateUser && method === 'PATCH') {
      const body = route.request().postDataJSON() as Partial<TestUser>;
      const id = Number(updateUser[1]);
      users = users.map((user) => user.id === id ? { ...user, ...body } : user);
      const user = users.find((item) => item.id === id);
      if (!user) return json({ code: 'user.not_found', error: 'The user was not found.' }, 404);
      return json(user);
    }

    unexpectedAPIRequests.push(`${method} ${path}`);
    return json({ error: `Unexpected API request: ${method} ${path}` }, 500);
  });

  await page.goto('/');
  await page.locator('.profile-menu-sidebar > summary').click();
  await expect(page.getByRole('button', { name: 'Administration' })).toBeVisible();
  await page.getByRole('button', { name: 'Administration' }).click();
  await expect(page.getByRole('heading', { name: 'Administration' })).toBeVisible();

  await expectNoHorizontalOverflow(page);
  await page.getByRole('button', { name: 'Feed requests' }).click();
  await expect(page.getByRole('heading', { name: 'Feed requests' })).toBeVisible();
  await expect(page.getByText('https://one.example/rss')).toBeVisible();
  await page.locator('.admin-table tbody tr').filter({ hasText: 'https://one.example/rss' }).getByRole('button', { name: 'Approve' }).click();
  await expect(page.getByText('https://one.example/rss')).toHaveCount(0);
  await page.locator('.admin-table tbody tr').filter({ hasText: 'https://two.example/rss' }).getByRole('button', { name: 'Reject' }).click();
  await expect(page.getByText('https://two.example/rss')).toHaveCount(0);
  await expectNoHorizontalOverflow(page);

  await page.getByRole('button', { name: 'Blocked words' }).click();
  await expect(page.getByRole('heading', { name: 'Blocked words and phrases' })).toBeVisible();
  await page.getByLabel('Word or phrase to block').fill('New phrase');
  await page.getByRole('button', { name: 'Add to dictionary' }).click();
  await expect(page.getByText('new phrase')).toBeVisible();
  await page.locator('.admin-table tbody tr').filter({ hasText: 'pornography' }).getByRole('button', { name: 'Remove' }).click();
  await expect(page.getByText('pornography')).toHaveCount(0);
  await expectNoHorizontalOverflow(page);

  await page.getByRole('button', { name: 'Blocked log' }).click();
  await expect(page.getByRole('heading', { name: 'Blocked articles log' })).toBeVisible();
  await expect(page.getByText('Blocked sample article')).toBeVisible();
  await expect(page.getByText('reader@example.com')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Allow', exact: true })).toBeVisible();
  await expect(page.getByText('Pending review', { exact: true })).toBeVisible();
  const screenshotLayout = await page.addStyleTag({ content: '.admin-workspace { grid-template-columns: minmax(0, 1fr) !important; } .admin-subnav { display: none !important; }' });
  await expect(page.locator('section[aria-labelledby="blocked-articles-title"]')).toHaveScreenshot('blocked-article-moderation.png');
  await screenshotLayout.evaluate((style) => style.parentNode?.removeChild(style));
  await page.getByRole('button', { name: 'Allow', exact: true }).click();
  await expect(page.getByText('Next pending article')).toBeVisible();
  await expect(page.getByText('Blocked sample article')).toHaveCount(0);
  await expectNoHorizontalOverflow(page);

  await page.getByRole('button', { name: 'Users' }).click();
  await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible();
  await page.getByLabel('New user email').fill('new@example.com');
  await page.getByLabel('Initial password').fill('a-long-test-password');
  await page.getByRole('button', { name: 'Create user' }).click();
  await expect(page.getByText('new@example.com', { exact: true })).toBeVisible();
  await page.getByLabel('Role for reader@example.com').selectOption('Administrator');
  await page.getByLabel('Interval for reader@example.com').selectOption('60');
  await expect(page.getByText('User interval saved.')).toBeVisible();
  await expectNoHorizontalOverflow(page);

  await page.getByRole('button', { name: 'Settings' }).click();
  await page.getByLabel('Scheduler interval, minutes').fill('30');
  await page.getByLabel('Shared source TTL, minutes').fill('45');
  await page.getByRole('button', { name: 'Save intervals' }).click();
  await expect(page.getByText('Settings saved.')).toBeVisible();
  await expectNoHorizontalOverflow(page);

  await page.setViewportSize({ width: 360, height: 780 });
  for (const section of ['Settings', 'Feed requests', 'Blocked words', 'Blocked log', 'Users']) {
    await page.getByRole('button', { name: section }).click();
    await expectNoHorizontalOverflow(page);
  }

  expect(apiRouteResponse.status(), `GET ${liveAPI}/api/admin/stop-words should reach the authenticated route, not a 404`).toBe(401);
  expect(unexpectedAPIRequests, 'Every UI API request should have a handled response').toEqual([]);
  expect(httpErrors, 'The browser UI should receive no failed HTTP responses').toEqual([]);
  expect(failedRequests, 'The browser UI should have no failed API network requests').toEqual([]);
  expect(pageErrors, 'The page should raise no uncaught JavaScript errors').toEqual([]);
  expect(consoleErrors, 'The browser console should contain no errors').toEqual([]);
});
