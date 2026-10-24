import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ArticleTitle } from '../../src/ArticleTitle';

afterEach(cleanup);

describe('ArticleTitle', () => {
  it('opens the local reader on a normal left click but preserves native source navigation', () => {
    const onOpenLocal = vi.fn();
    render(<ArticleTitle href="https://example.com/post" title="News headline" onOpenLocal={onOpenLocal} />);

    const link = screen.getByRole('link', { name: 'News headline' });
    expect(link.getAttribute('href')).toBe('https://example.com/post');
    expect(link.getAttribute('rel')).toBe('noreferrer');

    link.click();
    expect(onOpenLocal).toHaveBeenCalledTimes(1);
    onOpenLocal.mockClear();
    const nativeNavigations: Array<{ type: string; href: string | null; defaultPrevented: boolean }> = [];
    const captureNativeNavigation = (event: MouseEvent) => {
      nativeNavigations.push({ type: event.type, href: link.getAttribute('href'), defaultPrevented: event.defaultPrevented });
      event.preventDefault();
    };
    document.body.addEventListener('auxclick', captureNativeNavigation);
    document.body.addEventListener('click', captureNativeNavigation);
    link.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, cancelable: true, button: 1 }));
    link.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, ctrlKey: true }));
    document.body.removeEventListener('auxclick', captureNativeNavigation);
    document.body.removeEventListener('click', captureNativeNavigation);
    expect(onOpenLocal).not.toHaveBeenCalled();
    expect(nativeNavigations).toEqual([
      { type: 'auxclick', href: 'https://example.com/post', defaultPrevented: false },
      { type: 'click', href: 'https://example.com/post', defaultPrevented: false },
    ]);
  });
});
