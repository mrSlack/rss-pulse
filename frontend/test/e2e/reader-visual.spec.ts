import { expect, test, type Page } from '@playwright/test';

type MockOptions = {
  articleCount?: number;
  failArticles?: boolean;
  feedLimitScenario?: boolean;
};

function articlesBetween(offset: number, limit: number, articleCount: number) {
  return Array.from({ length: Math.max(0, Math.min(limit, articleCount - offset)) }, (_, index) => {
    const number = offset + index + 1;
    return {
      id: number,
      feedId: 1,
      feedTitle: 'The Daily Signal',
      feedUrl: 'https://example.com/feed.xml',
      title: `A considered headline for article ${number}`,
      url: `https://example.com/articles/${number}`,
      summary: 'A short, stable summary used to keep visual snapshots repeatable.',
      publishedAt: `2026-04-15T11:${String(59 - index).padStart(2, '0')}:00Z`,
      isRead: number === 2,
      isSaved: number === 3,
    };
  });
}

async function prepareReader(page: Page, options: MockOptions = {}) {
  const articleCount = options.articleCount ?? 3;
  let feeds: Array<{ id: number; title: string; url: string; folderId: number | null; folderName: string | null; unreadCount: number; lastCheckedAt: string; lastError: string | null }> = options.feedLimitScenario ? [] : [{
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
        const offset = Number(url.searchParams.get('offset') || 0);
        const limit = Number(url.searchParams.get('limit') || 20);
        const items = articlesBetween(offset, limit, articleCount);
        body = { items, hasMore: offset + items.length < articleCount };
      }
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
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1280, height: 900 });
  await prepareReader(page, { articleCount: 0, feedLimitScenario: true });
  let submittedCount = 0;
  page.on('request', (request) => {
    if (request.method() === 'POST' && new URL(request.url()).pathname.endsWith('/feeds')) {
      submittedCount += 1;
    }
  });
  await page.goto('/');
  const addFeedButton = page.getByRole('button', { name: 'Add feed' });
  for (let index = 1; index <= 100; index += 1) {
    await addFeedButton.click();
    const dialog = page.getByRole('dialog', { name: 'Add source' });
    await dialog.getByLabel('RSS feed URL').fill(`https://example.com/limit-test/${index}.xml`);
    await dialog.getByRole('button', { name: 'Subscribe' }).click();
    await expect(dialog).toBeHidden();
  }
  await expect(page.locator('.unfiled-folder .tree-feed-row .feed-name')).toHaveCount(100);
  expect(submittedCount).toBe(100);

  await addFeedButton.click();
  const dialog = page.getByRole('dialog', { name: 'Add source' });
  await dialog.getByLabel('RSS feed URL').fill('https://example.com/limit-test/101.xml');
  await dialog.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.locator('.error-toast-message')).toHaveText('You can subscribe to up to 100 feeds.');
  await expect(dialog).toBeVisible();
  expect(submittedCount).toBe(101);
  await expect(page.locator('.unfiled-folder .tree-feed-row .feed-name')).toHaveCount(100);
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
  const offsets: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.endsWith('/articles')) {
      offsets.push(new URL(request.url()).searchParams.get('offset') || '0');
    }
  });
  await page.goto('/');
  await expect(page.locator('.article-card')).toHaveCount(20);
  await page.locator('.load-more-wrap').scrollIntoViewIfNeeded();
  await expect(page.locator('.article-card')).toHaveCount(25);
  expect(offsets).toContain('20');
});
