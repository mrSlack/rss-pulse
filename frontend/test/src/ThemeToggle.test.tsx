import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ThemeToggle } from '../../src/ThemeToggle';

describe('ThemeToggle', () => {
  afterEach(cleanup);

  it('supports keyboard activation and labels the destination action', async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    render(<ThemeToggle theme="dark" onToggle={onToggle} />);

    const button = screen.getByRole('button', { name: 'Switch to light theme' });
    await user.tab();
    expect(document.activeElement).toBe(button);
    await user.keyboard('{Enter}');
    expect(onToggle).toHaveBeenCalledOnce();
  });
});
