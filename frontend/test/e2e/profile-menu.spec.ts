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
