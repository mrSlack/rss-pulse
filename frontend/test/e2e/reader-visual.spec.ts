import { expect, test, type Page } from '@playwright/test';

type MockOptions = {
  articleCount?: number;
  failArticles?: boolean;
  feedLimitScenario?: boolean;
};

function mockArticle(number: number, readNumbers: Set<number>) {
  return {
    id: number,
    feedId: 1,
    feedTitle: 'The Daily Signal',
    feedUrl: 'https://example.com/feed.xml',
    title: `A considered headline for article ${number}`,
    url: `https://example.com/articles/${number}`,
    summary: 'A short, stable summary used to keep visual snapshots repeatable.',
    publishedAt: `2026-04-15T11:${String(Math.max(0, 60 - number)).padStart(2, '0')}:00Z`,
    isRead: readNumbers.has(number),
    isSaved: number === 3,
  };
}

// Mimics the API's keyset paging: articles are ordered newest first (by
// ascending number here), and `before=c<number>` returns only articles after
// that one, evaluated against the current server-side read state.
function articlePage(url: URL, articleCount: number, readNumbers: Set<number>) {
  const limit = Number(url.searchParams.get('limit') || 20);
  const before = url.searchParams.get('before');
  const after = before ? Number(before.slice(1)) : 0;
  const matching = Array.from({ length: articleCount }, (_, index) => index + 1)
    .filter((number) => number > after)
    .filter((number) => url.searchParams.get('filter') !== 'unread' || !readNumbers.has(number));
  const items = matching.slice(0, limit).map((number) => mockArticle(number, readNumbers));
  const hasMore = matching.length > limit;
  return { items, hasMore, nextCursor: hasMore ? `c${items[items.length - 1].id}` : null, latestArticleId: articleCount };
}

async function prepareReader(page: Page, options: MockOptions = {}) {
  const articleCount = options.articleCount ?? 3;
  const readNumbers = new Set<number>([2]);
  // The limit scenario starts one below the 100-feed limit; adding each feed
  // through the dialog would take most of the test timeout.
  let feeds: Array<{ id: number; title: string; url: string; folderId: number | null; folderName: string | null; unreadCount: number; lastCheckedAt: string; lastError: string | null }> = options.feedLimitScenario ? Array.from({ length: 99 }, (_, index) => ({
    id: 99 - index,
    title: `Limit test feed ${99 - index}`,
    url: `https://example.com/limit-test/${99 - index}.xml`,
    folderId: null,
    folderName: null,
    unreadCount: 0,
    lastCheckedAt: '2026-04-15T11:55:00Z',
    lastError: null,
  })) : [{
    id: 1,
    title: 'The Daily Signal',
    url: 'https://example.com/feed.xml',
    folderId: 1,
    folderName: 'Reading list',
    unreadCount: articleCount,
    lastCheckedAt: '2026-04-15T11:55:00Z',
    lastError: null,
  }, {
    id: 2,
    title: 'Unfiled Brief',
    url: 'https://example.net/feed.xml',
    folderId: null,
    folderName: null,
    unreadCount: 0,
    lastCheckedAt: '2026-04-15T11:55:00Z',
    lastError: null,
  }];
  await page.addInitScript(() => {
    localStorage.setItem('pulse-theme', 'light');
    localStorage.setItem('pulse.language', 'en');
  });
  await page.clock.install({ time: new Date('2026-04-15T12:00:00Z') });
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    let body: unknown = [];
    let status = 200;

    if (path.endsWith('/auth/me')) {
      body = { id: 1, email: 'reader@example.com', role: 'Administrator', refreshIntervalMinutes: null };
    } else if (path.endsWith('/feeds')) {
      if (route.request().method() === 'POST' && options.feedLimitScenario) {
        if (feeds.length >= 100) {
          status = 409;
          body = {
            code: 'feed.limit_reached',
            error: 'You have reached the maximum of 100 feeds.',
          };
        } else {
          const request = route.request().postDataJSON() as { url: string; folderId: number | null };
          const feed = {
            id: feeds.length + 1,
            title: `Limit test feed ${feeds.length + 1}`,
            url: request.url,
            folderId: request.folderId,
            folderName: null,
            unreadCount: 0,
            lastCheckedAt: '2026-04-15T11:55:00Z',
            lastError: null,
          };
          feeds = [feed, ...feeds];
          status = 201;
          body = feed;
        }
      } else {
        body = feeds;
      }
    } else if (path.endsWith('/folders')) {
      body = [
        { id: 1, name: 'Reading list', color: 'amber', unreadCount: articleCount },
        { id: 2, name: 'Research', color: 'green', unreadCount: 0 },
        { id: 3, name: 'Longer name for design notes', color: 'violet', unreadCount: 0 },
      ];
    } else if (path.endsWith('/articles')) {
      if (options.failArticles) {
        status = 503;
        body = { code: 'test.articles_unavailable', error: 'Articles are temporarily unavailable.' };
      } else {
        body = articlePage(url, articleCount, readNumbers);
      }
    } else if (/\/articles\/\d+$/.test(path) && route.request().method() === 'PATCH') {
      const id = Number(path.split('/').pop());
      const update = route.request().postDataJSON() as { isRead?: boolean };
      if (update.isRead === true) readNumbers.add(id);
      if (update.isRead === false) readNumbers.delete(id);
      body = mockArticle(id, readNumbers);
    } else if (path.endsWith('/refresh')) {
      body = { running: false };
    }

    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
  });
}

async function expectUnreadMarkersAligned(page: Page) {
  const unreadCards = page.locator('.article-card.unread');
  for (let index = 0; index < await unreadCards.count(); index += 1) {
    const card = unreadCards.nth(index);
    const indicator = await card.locator('.article-unread-indicator').boundingBox();
    const sourceMark = await card.locator('.source-mark').boundingBox();
    expect(indicator).not.toBeNull();
    expect(sourceMark).not.toBeNull();
    expect(Math.abs((indicator!.y + indicator!.height / 2) - (sourceMark!.y + sourceMark!.height / 2))).toBeLessThan(1);
  }
}

test('reader feed matches its desktop visual baseline', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await prepareReader(page);
  await page.goto('/');
  await expect(page.locator('.article-card')).toHaveCount(3);
  await expectUnreadMarkersAligned(page);
  await expect(page.locator('.article-card .source-mark')).toHaveCount(3);
  await expect(page.locator('.article-card .source-mark').nth(0)).toHaveAttribute('class', 'source-mark source-mark-1');
  await expect(page.locator('.article-card .source-mark').nth(1)).toHaveAttribute('class', 'source-mark source-mark-1');
  await expect(page).toHaveScreenshot('reader-desktop.png', { fullPage: true, maxDiffPixelRatio: 0 });
});

test('mobile header keeps search collapsed and expands it over the header', async ({ page }) => {
  await page.setViewportSize({ width: 427, height: 952 });
  await prepareReader(page);
  await page.goto('/');

  const addButton = page.getByRole('button', { name: 'Add feed' });
  await expect(addButton).toBeVisible();
  await expect(addButton).toHaveCSS('width', '44px');
  await expect(page.locator('.topbar-search')).toBeHidden();
  await expect(page.getByRole('button', { name: 'Language' })).toBeVisible();
  await page.getByRole('button', { name: 'Language' }).click();
  const languageMenu = page.getByRole('menu', { name: 'Language' });
  await expect(languageMenu).toBeVisible();
  await expect(languageMenu.getByRole('menuitemradio')).toHaveCount(3);
  await languageMenu.getByRole('menuitemradio', { name: 'Русский' }).click();
  await expect(page.getByRole('heading', { name: 'Все статьи' })).toBeVisible();
  await page.getByRole('button', { name: 'Язык' }).click();
  await page.getByRole('menu', { name: 'Язык' }).getByRole('menuitemradio', { name: 'English' }).click();
  await expect(page.getByRole('button', { name: 'Search articles' })).toHaveAttribute('aria-expanded', 'false');
  await expectUnreadMarkersAligned(page);
  await expect(page).toHaveScreenshot('reader-mobile-427.png', { fullPage: true, maxDiffPixelRatio: 0 });

  await page.getByRole('button', { name: 'Search articles' }).click();
  const searchInput = page.getByRole('textbox', { name: 'Search articles' });
  await expect(searchInput).toBeFocused();
  await expect(page.getByRole('button', { name: 'Search articles' })).toHaveAttribute('aria-expanded', 'true');
  await expect(page.locator('.mobile-search-panel')).toBeVisible();
  await page.waitForTimeout(250);
  const searchPanel = await page.locator('.mobile-search-panel').boundingBox();
  expect(searchPanel).not.toBeNull();
  expect(searchPanel!.width).toBeGreaterThan(390);
  await expect(page).toHaveScreenshot('reader-mobile-search-427.png', { fullPage: true });
  await searchInput.fill('mobile query');
  await searchInput.press('Escape');
  await expect(page.locator('.mobile-search-panel')).toBeHidden();
});

test('the 724px add-source dialog and folder picker fit the viewport', async ({ page }) => {
  await page.setViewportSize({ width: 724, height: 1014 });
  await prepareReader(page);
  await page.goto('/');
  await page.getByRole('button', { name: 'Add feed' }).click();

  const dialog = page.getByRole('dialog', { name: 'Add source' });
  const folderPicker = dialog.getByRole('combobox', { name: 'Folder optional' });
  await expect(dialog).toBeVisible();
  await folderPicker.click();
  const folderList = page.getByRole('listbox', { name: 'Folder optional' });
  await expect(folderList).toBeVisible();
  await expect(folderList.getByRole('option')).toHaveCount(4);
  const listBox = await folderList.boundingBox();
  expect(listBox).not.toBeNull();
  expect(listBox!.x).toBeGreaterThanOrEqual(0);
  expect(listBox!.x + listBox!.width).toBeLessThanOrEqual(724);
  expect(listBox!.y).toBeGreaterThanOrEqual(0);
  expect(listBox!.y + listBox!.height).toBeLessThanOrEqual(1014);
  await expect(page).toHaveScreenshot('add-source-folder-picker-724.png', { fullPage: true, maxDiffPixelRatio: 0 });
  await folderList.getByRole('option', { name: 'Research' }).click();
  await expect(folderPicker).toContainText('Research');
});

test('adding a feed at the subscription limit shows the limit error', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page, { articleCount: 0, feedLimitScenario: true });
  let submittedCount = 0;
  page.on('request', (request) => {
    if (request.method() === 'POST' && new URL(request.url()).pathname.endsWith('/feeds')) {
      submittedCount += 1;
    }
  });
  await page.goto('/');
  const feedNames = page.locator('.unfiled-folder .tree-feed-row .feed-name');
  await expect(feedNames).toHaveCount(99);

  // The 100th feed is still accepted.
  const addFeedButton = page.getByRole('button', { name: 'Add feed' });
  await addFeedButton.click();
  const dialog = page.getByRole('dialog', { name: 'Add source' });
  await dialog.getByLabel('RSS feed URL').fill('https://example.com/limit-test/100.xml');
  await dialog.getByRole('button', { name: 'Subscribe' }).click();
  await expect(dialog).toBeHidden();
  await expect(feedNames).toHaveCount(100);
  expect(submittedCount).toBe(1);

  // The 101st is refused, and the dialog stays open with the server's error.
  await addFeedButton.click();
  await dialog.getByLabel('RSS feed URL').fill('https://example.com/limit-test/101.xml');
  await dialog.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.locator('.error-toast-message')).toHaveText('You can subscribe to up to 100 feeds.');
  await expect(dialog).toBeVisible();
  expect(submittedCount).toBe(2);
  await expect(feedNames).toHaveCount(100);
});

test('feed sources are nested under folders and can be collapsed', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page);
  await page.goto('/');

  const folderGroup = page.getByRole('group', { name: 'Reading list' });
  const feedButton = folderGroup.locator('.tree-feed-row .feed-item');
  await expect(folderGroup).toBeVisible();
  await expect(page.getByRole('button', { name: 'Research' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Longer name for design notes' })).toHaveCount(0);
  await expect(feedButton).toContainText('The Daily Signal');
  await expect(page.locator('.unfiled-folder .tree-feed-row .feed-name')).toHaveText('Unfiled Brief');
  await page.getByRole('button', { name: 'Collapse Reading list' }).click();
  await expect(folderGroup).toBeHidden();
  await page.getByRole('button', { name: 'Expand Reading list' }).click();
  await expect(folderGroup).toBeVisible();
  await expect(feedButton).toBeVisible();
});

test('a reader can rename a folder and choose its color', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await prepareReader(page);
  let updateBody: unknown;
  await page.route('**/api/folders/1', async (route) => {
    updateBody = route.request().postDataJSON();
    const updatedFolder = updateBody as { name: string; color: string };
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ id: 1, ...updatedFolder, unreadCount: 3 }),
    });
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Manage folders' }).click();
  await expect(page.getByRole('dialog', { name: 'Manage folders' })).toBeVisible();
  await expect(page).toHaveScreenshot('folder-management.png', { fullPage: true });
  await expect(page.getByRole('button', { name: 'Rename Research' })).toBeVisible();
  await page.getByRole('button', { name: 'Rename Reading list' }).click();

  const dialog = page.getByRole('dialog', { name: 'Edit folder' });
  await expect(dialog).toBeVisible();
  await expect(page).toHaveScreenshot('edit-folder-modal.png', { fullPage: true, maxDiffPixelRatio: 0 });
  await dialog.getByLabel('Folder name').fill('News desk');
  await dialog.getByRole('radio', { name: 'Blue folder color' }).click();
  await dialog.getByRole('button', { name: 'Save changes' }).click();

  await expect(page.locator('.folder-item').filter({ hasText: 'News desk' })).toBeVisible();
  await expect(page.locator('.folder-row').first().locator('.folder-dot')).toHaveCount(0);
  expect(updateBody).toEqual({ name: 'News desk', color: 'blue' });

  await page.getByRole('button', { name: 'Manage folders' }).click();
  await page.getByRole('button', { name: 'Rename News desk' }).click();
  const editDialog = page.getByRole('dialog', { name: 'Edit folder' });
  await editDialog.getByLabel('Custom color').fill('#12abef');
  await editDialog.getByRole('button', { name: 'Save changes' }).click();
  const updatedFolderRow = page.locator('.folder-row').first();
  await expect(updatedFolderRow.locator('.folder-dot')).toHaveCount(0);
  expect(updateBody).toEqual({ name: 'News desk', color: '#12abef' });
});

test('folder management can move sources before deleting a populated folder', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page);
  let deleteBody: unknown;
  await page.route('**/api/folders/1', async (route) => {
    expect(route.request().method()).toBe('DELETE');
    deleteBody = route.request().postDataJSON();
    await route.fulfill({ status: 204 });
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Manage folders' }).click();
  await page.getByRole('button', { name: 'Delete Reading list' }).click();

  const dialog = page.getByRole('dialog', { name: 'Delete folder' });
  await expect(dialog).toContainText('contains 1 source.');
  await expect(page).toHaveScreenshot('folder-delete-warning.png', { fullPage: true });
  await dialog.getByLabel('Move sources to another folder').check();
  await dialog.getByLabel('Move to folder').selectOption({ label: 'Research' });
  await dialog.getByRole('button', { name: 'Delete folder' }).click();

  await expect(page.getByRole('button', { name: 'Reading list' })).toHaveCount(0);
  await expect(page.getByRole('group', { name: 'Research' })).toContainText('The Daily Signal');
  expect(deleteBody).toEqual({ action: 'move_sources', targetFolderId: 2 });
});

test('folder management can remove its subscriptions after confirmation', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page);
  let deleteBody: unknown;
  await page.route('**/api/folders/1', async (route) => {
    expect(route.request().method()).toBe('DELETE');
    deleteBody = route.request().postDataJSON();
    await route.fulfill({ status: 204 });
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Manage folders' }).click();
  await page.getByRole('button', { name: 'Delete Reading list' }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete folder' });
  await dialog.getByLabel('Remove sources from my library').check();
  await dialog.getByRole('button', { name: 'Delete folder' }).click();

  await expect(page.getByRole('button', { name: 'Reading list' })).toHaveCount(0);
  await expect(page.locator('.tree-feed-row .feed-name', { hasText: 'The Daily Signal' })).toHaveCount(0);
  expect(deleteBody).toEqual({ action: 'delete_sources' });
});

test('article loading errors match the visible error notification', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page, { failArticles: true });
  await page.goto('/');
  await expect(page.getByRole('alert')).toContainText('Articles are temporarily unavailable.');
  await expect(page).toHaveScreenshot('article-load-error.png', { fullPage: true });
});

test('scrolling near the end automatically loads the next 20 articles', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page, { articleCount: 25 });
  const cursors: Array<string | null> = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.endsWith('/articles')) {
      cursors.push(new URL(request.url()).searchParams.get('before'));
    }
  });
  await page.goto('/');
  await expect(page.locator('.article-card')).toHaveCount(20);
  await page.locator('.load-more-wrap').scrollIntoViewIfNeeded();
  await expect(page.locator('.article-card')).toHaveCount(25);
  expect(cursors).toEqual([null, 'c20']);
});

test('the unread view keeps every unread article when some are read between pages', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page, { articleCount: 45 });
  const articleRequests: URL[] = [];
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (url.pathname.endsWith('/articles')) articleRequests.push(url);
  });
  await page.goto('/');
  const cards = page.locator('.article-card');
  await expect(cards).toHaveCount(20);
  await page.getByRole('region', { name: 'Article list' }).getByRole('button', { name: 'Unread', exact: true }).click();
  await expect(page.locator('.unread-toggle.checked')).toBeVisible();
  await expect.poll(() => articleRequests.filter((url) => url.searchParams.get('filter') === 'unread').length).toBe(1);
  await expect(cards).toHaveCount(20);
  await expect(cards.nth(1)).toContainText('article 3');
  await expect(cards.first()).toContainText('article 1');

  for (let index = 0; index < 6; index += 1) {
    await cards.nth(index).getByRole('button', { name: 'Mark as read' }).click();
    await expect(cards.nth(index).getByRole('button', { name: 'Mark as unread' })).toBeVisible();
  }
  await expect(cards).toHaveCount(20);

  await page.locator('.load-more-wrap').scrollIntoViewIfNeeded();
  await expect(cards).toHaveCount(40);
  await page.locator('.load-more-wrap').scrollIntoViewIfNeeded();
  await expect(cards).toHaveCount(44);
  await expect(page.locator('.load-more-wrap')).toHaveCount(0);

  const titles = await cards.locator('h2').allTextContents();
  const numbers = titles.map((title) => Number(title.match(/article (\d+)$/)?.[1]));
  const expected = Array.from({ length: 45 }, (_, index) => index + 1).filter((number) => number !== 2);
  expect(numbers).toEqual(expected);
  const listRequests = articleRequests.filter((url) => url.searchParams.get('filter') === 'unread');
  expect(listRequests.map((url) => url.searchParams.get('before'))).toEqual([null, 'c21', 'c41']);
  expect(listRequests.every((url) => !url.searchParams.has('offset'))).toBe(true);
});

test('mark all as read marks the current view up to the articles that existed when it loaded', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page);
  let markBody: unknown;
  await page.route('**/api/articles/mark-read', async (route) => {
    markBody = route.request().postDataJSON();
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ marked: 2 }) });
  });
  const articleRequests: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.endsWith('/articles')) articleRequests.push(request.url());
  });
  await page.goto('/');
  await expect(page.locator('.article-card.unread')).toHaveCount(2);

  await page.getByRole('button', { name: 'Mark all as read' }).click();
  await expect(page.locator('.article-card.unread')).toHaveCount(0);
  await expect(page.locator('.article-card')).toHaveCount(3);
  expect(markBody).toEqual({ filter: 'all', upToId: 3 });
  expect(articleRequests).toHaveLength(1);
  await expect(page.getByRole('button', { name: 'Mark all as read' })).toBeDisabled();
});

test('keyboard shortcuts move through articles and open the help dialog', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page);
  await page.goto('/');
  await expect(page.locator('.article-card')).toHaveCount(3);

  await page.keyboard.press('j');
  const reader = page.getByRole('dialog', { name: 'Article' });
  await expect(reader.getByRole('heading', { name: 'A considered headline for article 1' })).toBeVisible();
  await page.keyboard.press('j');
  await expect(reader.getByRole('heading', { name: 'A considered headline for article 2' })).toBeVisible();
  await page.keyboard.press('k');
  await expect(reader.getByRole('heading', { name: 'A considered headline for article 1' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(reader).toBeHidden();

  await page.keyboard.press('/');
  const search = page.getByRole('textbox', { name: 'Search articles' });
  await expect(search).toBeFocused();
  await page.keyboard.type('jk');
  await expect(search).toHaveValue('jk');
  await expect(reader).toBeHidden();
  await search.fill('');
  await search.blur();

  await page.keyboard.press('?');
  const help = page.getByRole('dialog', { name: 'Keyboard shortcuts' });
  await expect(help).toBeVisible();
  await expect(help.getByRole('button', { name: 'Close' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(help.getByRole('button', { name: 'Close' })).toBeFocused();
  await expect(page).toHaveScreenshot('keyboard-shortcuts.png', { fullPage: true });
  await page.keyboard.press('j');
  await expect(reader).toBeHidden();
  await page.keyboard.press('Escape');
  await expect(help).toBeHidden();
});

test('subscriptions can be exported and imported as OPML', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page);
  let importRequest: { contentType: string | undefined; body: string | null } | undefined;
  await page.route('**/api/opml', async (route) => {
    importRequest = { contentType: route.request().headers()['content-type'], body: route.request().postData() };
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ subscribed: 1, requested: 2, skipped: 1, failed: [{ url: 'not a url', reason: 'feed.url_invalid' }] }),
    });
  });
  await page.goto('/');
  await page.locator('.profile-menu-topbar > summary').click();
  const menu = page.locator('.profile-menu-topbar .profile-dropdown');
  await expect(menu.getByRole('link', { name: 'Export subscriptions' })).toHaveAttribute('href', '/api/opml');
  await menu.getByRole('button', { name: 'Import subscriptions' }).click();

  const dialog = page.getByRole('dialog', { name: 'Import subscriptions' });
  const opmlDocument = '<?xml version="1.0"?><opml version="2.0"><body><outline text="News"><outline text="Feed" xmlUrl="https://example.org/rss"/></outline></body></opml>';
  await dialog.getByLabel('OPML file').setInputFiles({ name: 'feeds.opml', mimeType: 'text/x-opml', buffer: Buffer.from(opmlDocument) });
  await dialog.getByRole('button', { name: 'Import' }).click();

  const summary = dialog.getByRole('status');
  await expect(summary).toContainText('1 feed added to your library.');
  await expect(summary).toContainText('2 feeds sent for administrator approval.');
  await expect(summary).toContainText('1 feed skipped');
  await expect(summary).toContainText('not a url — Enter a valid HTTP or HTTPS feed URL.');
  expect(importRequest).toEqual({ contentType: 'text/x-opml', body: opmlDocument });
});
