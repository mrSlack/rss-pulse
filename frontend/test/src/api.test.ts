import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, extractArticleImages, folderColorClass, folderColorStyle } from '../../src/api';

afterEach(() => vi.unstubAllGlobals());

describe('authenticated API', () => {
  it('loads the current user with same-origin cookies', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 7, email: 'reader@example.com', role: 'Reader' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.currentUser()).resolves.toMatchObject({ id: 7, role: 'Reader' });
    expect(fetchMock).toHaveBeenCalledWith('/api/auth/me', expect.objectContaining({ credentials: 'same-origin' }));
  });

  it('submits login credentials without persisting a session token in frontend code', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 1, email: 'admin@example.com', role: 'Administrator' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await api.login('admin@example.com', 'a-long-password');
    expect(fetchMock).toHaveBeenCalledWith('/api/auth/login', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({ email: 'admin@example.com', password: 'a-long-password' }),
    }));
  });

  it('updates a personal folder name and color through the authenticated API', async () => {
    const folder = { id: 7, name: 'News desk', color: 'blue', unreadCount: 2 };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(folder), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.updateFolder(7, { name: folder.name, color: 'blue' })).resolves.toEqual(folder);
    expect(fetchMock).toHaveBeenCalledWith('/api/folders/7', expect.objectContaining({
      method: 'PATCH',
      credentials: 'same-origin',
      body: JSON.stringify({ name: folder.name, color: 'blue' }),
    }));
  });
});

describe('folder colors', () => {
  it('keeps preset colors and exposes valid custom HEX colors for display', () => {
    expect(folderColorClass('blue')).toBe('folder-color-blue');
    expect(folderColorClass('#12abef')).toBe('folder-color-custom');
    expect(folderColorStyle('#12abef')).toEqual({ backgroundColor: '#12abef' });
    expect(folderColorStyle('not-a-color')).toBeUndefined();
  });
});

describe('extractArticleImages', () => {
  it('extracts unique HTTP(S) images and resolves relative URLs against the article', () => {
    const images = extractArticleImages(`
      <p>Article text</p>
      <img src="/media/cover.jpg" alt="Cover">
      <img data-src="https://cdn.example.com/inside.jpg">
      <img src="/media/cover.jpg">
      <img src="javascript:alert(1)">
      <img src="data:image/svg+xml,evil">
      <img srcset="https://cdn.example.com/small.jpg 1x, https://cdn.example.com/large.jpg 2x">
    `, 'https://example.com/news/one');

    expect(images).toEqual([
      { src: 'https://example.com/media/cover.jpg', alt: 'Cover' },
      { src: 'https://cdn.example.com/inside.jpg', alt: '' },
      { src: 'https://cdn.example.com/large.jpg', alt: '' },
    ]);
  });

  it('returns no images for plain text or invalid article URLs', () => {
    expect(extractArticleImages('Plain text', 'https://example.com/news')).toEqual([]);
    expect(extractArticleImages('<img src="/image.jpg">', 'not a URL')).toEqual([]);
  });
});
