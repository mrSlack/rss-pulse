import { request, type FullConfig } from '@playwright/test';
import { e2eAccounts } from './test-fixtures';

export default async function globalSetup(_config: FullConfig) {
  const apiURL = process.env.PLAYWRIGHT_API_URL;
  if (!apiURL) throw new Error('PLAYWRIGHT_API_URL is required when PLAYWRIGHT_SEED_TEST_USERS=true');

  const api = await request.newContext({ baseURL: apiURL });
  const origin = new URL(apiURL).origin;
  const adminLogin = await api.post('/api/auth/login', {
    data: e2eAccounts.admin,
    headers: { Origin: origin },
  });
  if (!adminLogin.ok()) {
    throw new Error(`Could not sign in as the E2E administrator (${adminLogin.status()})`);
  }

  const readerLogin = await api.post('/api/auth/login', {
    data: e2eAccounts.reader,
    headers: { Origin: origin },
  });
  if (!readerLogin.ok()) {
    const createReader = await api.post('/api/admin/users', {
      data: { ...e2eAccounts.reader, role: 'Reader' },
      headers: { Origin: origin },
    });
    if (!createReader.ok()) {
      throw new Error(`Could not seed the E2E reader account (${createReader.status()}): ${await createReader.text()}`);
    }
  }

  await api.dispose();
}
