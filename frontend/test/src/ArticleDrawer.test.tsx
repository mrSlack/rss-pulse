import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ArticleDrawer } from '../../src/ArticleDrawer';

afterEach(cleanup);

describe('ArticleDrawer', () => {
  it('renders article content in an accessible dialog', () => {
    render(<ArticleDrawer onClose={vi.fn()}><h2>News headline</h2></ArticleDrawer>);

    expect(screen.getByRole('dialog', { name: 'Article' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'News headline' })).toBeTruthy();
  });

  it('closes from the close button, Escape, and backdrop', () => {
    const onClose = vi.fn();
    render(<ArticleDrawer onClose={onClose}><p>Article body</p></ArticleDrawer>);

    fireEvent.click(screen.getByRole('button', { name: 'Close article' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(2);
    fireEvent.mouseDown(document.querySelector('.article-drawer-backdrop')!);
    expect(onClose).toHaveBeenCalledTimes(3);
  });

  it('moves focus into the drawer, traps Tab, and restores focus when unmounted', () => {
    const opener = document.createElement('button');
    document.body.append(opener);
    opener.focus();
    const { unmount } = render(<ArticleDrawer onClose={vi.fn()}><button>Article action</button></ArticleDrawer>);
    const close = screen.getByRole('button', { name: 'Close article' });
    const action = screen.getByRole('button', { name: 'Article action' });

    expect(document.activeElement).toBe(close);
    fireEvent.keyDown(close, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(action);
    fireEvent.keyDown(action, { key: 'Tab' });
    expect(document.activeElement).toBe(close);

    unmount();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });
});
