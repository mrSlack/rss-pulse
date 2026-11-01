import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SignIn } from '../../src/SignIn';

afterEach(cleanup);

describe('SignIn', () => {
  it('submits email and password and shows authentication errors', async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn().mockRejectedValue(new Error('The email address or password is incorrect.'));
    render(<SignIn onLogin={onLogin} />);

    await user.type(screen.getByLabelText('Email'), 'reader@example.com');
    await user.type(screen.getByLabelText('Password'), 'a-long-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(onLogin).toHaveBeenCalledWith('reader@example.com', 'a-long-password', false);
    expect((await screen.findByRole('alert')).textContent).toContain('The email address or password is incorrect.');
  });

  it('offers to remember the device for offline use, off by default in a browser tab', async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn().mockResolvedValue(undefined);
    render(<SignIn onLogin={onLogin} />);

    const box = screen.getByRole('checkbox', { name: 'Remember me on this device for offline use' }) as HTMLInputElement;
    expect(box.checked).toBe(false);
    expect(box.getAttribute('aria-describedby')).toBeTruthy();
    expect(document.getElementById(box.getAttribute('aria-describedby')!)?.textContent).toContain('shared computers');
    await user.type(screen.getByLabelText('Email'), 'reader@example.com');
    await user.type(screen.getByLabelText('Password'), 'a-long-password');
    await user.click(box);
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(onLogin).toHaveBeenCalledWith('reader@example.com', 'a-long-password', true);
  });

  it('disables the submit button while a login is pending', () => {
    render(<SignIn onLogin={() => new Promise(() => undefined)} busy />);
    expect((screen.getByRole('button', { name: 'Signing in…' }) as HTMLButtonElement).disabled).toBe(true);
  });
});
