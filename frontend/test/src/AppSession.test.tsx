import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from '../../src/App';

function json(body: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('expired sessions', () => {
  it('returns to sign-in with a notice when an authenticated request reports 401', async () => {
    vi.stubGlobal('fetch', vi.fn((input: string) => {
      const path = new URL(input, 'http://localhost').pathname;
      if (path === '/api/auth/me') return json({ id: 3, email: 'reader@example.com', role: 'Reader', refreshIntervalMinutes: null });
      if (path === '/api/feeds' || path === '/api/folders') return json([]);
      return json({ code: 'auth.session_expired', error: 'Your session has expired.' }, 401);
    }));
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Welcome back' })).toBeTruthy();
    expect(screen.getByRole('status').textContent).toBe('Your session has expired. Please sign in again.');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows sign-in without a notice when there is no session yet', async () => {
    vi.stubGlobal('fetch', vi.fn(() => json({ code: 'auth.sign_in_required', error: 'Please sign in.' }, 401)));
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Welcome back' })).toBeTruthy();
    expect(screen.queryByRole('status')).toBeNull();
  });
});
