import { expect, test } from '@playwright/test';
import { e2eAccounts } from './test-fixtures';

test.skip(process.env.PLAYWRIGHT_REAL_E2E !== 'true', 'Run with the isolated E2E compose stack to exercise real accounts and API.');

test('reader and administrator sign in and complete their main workflows', async ({ page }) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  const httpErrors: string[] = [];
  const failedRequests: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  page.on('console', (message) => {
    if (message.type() === 'error' && !message.text().startsWith('Failed to load resource:')) consoleErrors.push(message.text());
  });
  page.on('response', (response) => {
    if (response.status() >= 400 && !(response.url().endsWith('/api/auth/me') && response.status() === 401)) {
      httpErrors.push(`${response.status()} ${response.request().method()} ${response.url()}`);
    }
  });
  page.on('requestfailed', (request) => failedRequests.push(`${request.method()} ${request.url()}: ${request.failure()?.errorText}`));

  async function signIn(account: typeof e2eAccounts.admin | typeof e2eAccounts.reader) {
    await page.getByLabel('Email').fill(account.email);
    await page.getByLabel('Password').fill(account.password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.locator('.profile-menu-sidebar > summary')).toBeVisible();
  }

  async function signOut() {
    await page.locator('.profile-menu-sidebar > summary').click();
    await page.getByRole('button', { name: 'Sign out' }).click();
    await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
  }

  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();

  await signIn(e2eAccounts.reader);
  await expect(page.locator('.profile-menu-sidebar > summary')).toContainText('Reader');
  await expect(page.getByRole('button', { name: 'Administration' })).toHaveCount(0);
  const forbiddenAdminResponse = await page.request.get('/api/admin/settings');
  expect(forbiddenAdminResponse.status()).toBe(403);

  const unique = Date.now();
  await page.getByRole('button', { name: 'Create folder' }).click();
  const folderDialog = page.getByRole('dialog', { name: 'New folder' });
  await folderDialog.getByLabel('Folder name').fill(`Browser test ${unique}`);
  await folderDialog.getByRole('button', { name: 'Create folder' }).click();
  await page.getByRole('button', { name: 'Manage folders' }).click();
  const foldersDialog = page.getByRole('dialog', { name: 'Manage folders' });
  await expect(foldersDialog).toContainText(`Browser test ${unique}`);
  await foldersDialog.getByRole('button', { name: 'Close' }).click();

  await page.getByRole('button', { name: 'Add feed' }).click();
  await page.getByLabel('RSS feed URL').fill(`https://browser-test-${unique}.invalid/rss`);
  await page.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.getByRole('status')).toContainText('Request submitted.');
  await page.getByRole('button', { name: 'Close' }).click();
  await signOut();

  await signIn(e2eAccounts.admin);
  await expect(page.locator('.profile-menu-sidebar > summary')).toContainText('Administrator');
  await page.locator('.profile-menu-sidebar > summary').click();
  await page.getByRole('button', { name: 'Administration' }).click();
  await expect(page.getByRole('heading', { name: 'Feed updates' })).toBeVisible();

  await page.getByRole('button', { name: 'Feed requests' }).click();
  const requestRow = page.locator('.admin-table tbody tr').filter({ hasText: `browser-test-${unique}.invalid` });
  await expect(requestRow).toBeVisible();
  await expect(requestRow).toContainText(e2eAccounts.reader.email);
  await requestRow.getByRole('button', { name: 'Reject' }).click();
  await expect(page.getByRole('status')).toContainText('Feed request rejected.');

  await page.getByRole('button', { name: 'Blocked words' }).click();
  await page.getByLabel('Word or phrase to block').fill(`browser-test-${unique}`);
  await page.getByRole('button', { name: 'Add to dictionary' }).click();
  const blockedWordRow = page.locator('.admin-table tbody tr').filter({ hasText: `browser test ${unique}` });
  await expect(blockedWordRow).toBeVisible();
  await blockedWordRow.getByRole('button', { name: 'Remove' }).click();
  await expect(blockedWordRow).toHaveCount(0);

  await page.getByRole('button', { name: 'Blocked log' }).click();
  await expect(page.getByRole('heading', { name: 'Blocked articles log' })).toBeVisible();
  await page.getByRole('button', { name: 'Users' }).click();
  await expect(page.getByText(e2eAccounts.reader.email, { exact: true })).toBeVisible();

  const dimensions = await page.evaluate(() => ({
    width: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(dimensions.scrollWidth, JSON.stringify(dimensions)).toBeLessThanOrEqual(dimensions.width);
  expect(httpErrors).toEqual([]);
  expect(failedRequests).toEqual([]);
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
});
