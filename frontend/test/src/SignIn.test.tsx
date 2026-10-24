import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { SignIn } from '../../src/SignIn';

describe('SignIn', () => {
  it('submits email and password and shows authentication errors', async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn().mockRejectedValue(new Error('The email address or password is incorrect.'));
    render(<SignIn onLogin={onLogin} />);

    await user.type(screen.getByLabelText('Email'), 'reader@example.com');
    await user.type(screen.getByLabelText('Password'), 'a-long-password');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(onLogin).toHaveBeenCalledWith('reader@example.com', 'a-long-password');
    expect((await screen.findByRole('alert')).textContent).toContain('The email address or password is incorrect.');
  });

  it('disables the submit button while a login is pending', () => {
    render(<SignIn onLogin={() => new Promise(() => undefined)} busy />);
    expect((screen.getByRole('button', { name: 'Signing in…' }) as HTMLButtonElement).disabled).toBe(true);
  });
});
