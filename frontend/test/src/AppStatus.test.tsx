import { act, cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import '../../src/i18n';

const pwa = vi.hoisted(() => {
  let available = false;
  const listeners = new Set<() => void>();
  return {
    applyUpdate: vi.fn(),
    updateAvailable: () => available,
    subscribeToUpdates: (listener: () => void) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    announce(value: boolean) {
      available = value;
      listeners.forEach((listener) => listener());
    },
  };
});

vi.mock('../../src/pwa', () => pwa);

const { AppStatus } = await import('../../src/AppStatus');

afterEach(() => {
  cleanup();
  pwa.announce(false);
  vi.clearAllMocks();
});

describe('new version prompt', () => {
  it('offers a reload once a new version is waiting', async () => {
    const user = userEvent.setup();
    render(<AppStatus signedIn />);
    expect(screen.queryByRole('status')).toBeNull();

    act(() => pwa.announce(true));
    expect(screen.getByRole('status').textContent).toContain('A new version of Pulse is available.');
    await user.click(screen.getByRole('button', { name: 'Reload' }));
    expect(pwa.applyUpdate).toHaveBeenCalledOnce();
  });
});
