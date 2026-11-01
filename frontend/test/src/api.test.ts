import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, REQUEST_TIMEOUT_MS, SESSION_CHECK_TIMEOUT_MS, api, extractArticleImages, folderColorClass, folderColorStyle, isAbortError, setUnauthorizedHandler } from '../../src/api';
import i18n from '../../src/i18n';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  setUnauthorizedHandler(null);
});

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('typed API errors', () => {
  it('throws an ApiError with the status, stable code and localized message', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ code: 'feed.limit_reached', error: 'Limit.' }, 409)));
    const error = await api.getFeeds().catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 409, code: 'feed.limit_reached', message: i18n.t('apiErrors.feed.limit_reached') });
  });

  it('reports network failures as an ApiError with status 0', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    const error = await api.getFeeds().catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 0, code: 'network', message: i18n.t('apiErrors.network') });
  });

  it('passes an AbortSignal to fetch and rethrows aborts as abort errors', async () => {
    const fetchMock = vi.fn((_url: string, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')));
    }));
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();
    const pending = api.getArticles(new URLSearchParams({ filter: 'all' }), controller.signal).catch((reason: unknown) => reason);
    controller.abort();
    const error = await pending;
    expect(fetchMock.mock.calls[0][1]?.signal?.aborted).toBe(true);
    expect(isAbortError(error)).toBe(true);
    expect(error).not.toBeInstanceOf(ApiError);
  });
});

/** A fetch that never answers until its signal is aborted. */
function hangingFetch() {
  return vi.fn((_url: string, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
    init?.signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')));
  }));
}

function observe(promise: Promise<unknown>) {
  const state: { error?: unknown; settled: boolean } = { settled: false };
  promise.then(() => { state.settled = true; }, (error: unknown) => { state.error = error; state.settled = true; });
  return state;
}

describe('request timeouts', () => {
  it('outlasts the proxy and server time limits so only a hanging connection times out', () => {
    // nginx.conf proxy_read_timeout 30s; the API's WriteTimeout is 25s.
    expect(REQUEST_TIMEOUT_MS).toBeGreaterThan(30_000);
    expect(SESSION_CHECK_TIMEOUT_MS).toBeLessThan(REQUEST_TIMEOUT_MS);
  });

  it('fails a request that gets no response with a timeout error', async () => {
    vi.useFakeTimers();
    const fetchMock = hangingFetch();
    vi.stubGlobal('fetch', fetchMock);
    const state = observe(api.getFeeds());

    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUT_MS - 1);
    expect(state.settled).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(state.error).toBeInstanceOf(ApiError);
    expect(state.error).toMatchObject({ status: 0, code: 'timeout', message: i18n.t('apiErrors.timeout') });
    expect(fetchMock.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it('also limits changes, so a hanging save does not spin forever', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', hangingFetch());
    const state = observe(api.updateArticle(1, { isRead: true }));
    await vi.advanceTimersByTimeAsync(REQUEST_TIMEOUT_MS);
    expect(state.error).toMatchObject({ code: 'timeout' });
  });

  it('gives up on the session check sooner, so a hanging start falls back to offline mode', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', hangingFetch());
    const state = observe(api.currentUser());
    await vi.advanceTimersByTimeAsync(SESSION_CHECK_TIMEOUT_MS);
    expect(state.error).toMatchObject({ status: 0, code: 'timeout' });
  });

  it('does not limit an OPML import, whose upload time depends on the file and the link', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', hangingFetch());
    const controller = new AbortController();
    const state = observe(api.importOpml('<opml/>', controller.signal));
    await vi.advanceTimersByTimeAsync(10 * REQUEST_TIMEOUT_MS);
    expect(state.settled).toBe(false);
    controller.abort();
    await vi.advanceTimersByTimeAsync(0);
    expect(isAbortError(state.error)).toBe(true);
  });

  it('reports a cancelled request as an abort, not as a timeout, and stops its timer', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', hangingFetch());
    const controller = new AbortController();
    const state = observe(api.getArticles(new URLSearchParams(), controller.signal));
    controller.abort();
    await vi.advanceTimersByTimeAsync(0);
    expect(isAbortError(state.error)).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('honours a signal that was aborted before the request started', async () => {
    const fetchMock = hangingFetch();
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();
    controller.abort();
    const pending = api.getFeeds(controller.signal).catch((reason: unknown) => reason);
    // The hanging fetch only rejects on an abort event, so check the signal it got.
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(fetchMock.mock.calls[0][1]?.signal?.aborted).toBe(true);
    void pending;
  });

  it('stops the timer once a response has been read', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse([])));
    await expect(api.getFeeds()).resolves.toEqual([]);
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe('expired sessions', () => {
  it('notifies the unauthorized handler for an authenticated call that returns 401', async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ code: 'auth.session_expired', error: 'Expired.' }, 401)));
    await expect(api.getFolders()).rejects.toMatchObject({ status: 401 });
    expect(onUnauthorized).toHaveBeenCalledOnce();
  });

  it('does not treat the session probe or a failed sign-in as an expired session', async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ code: 'auth.invalid_credentials', error: 'No.' }, 401))));
    await expect(api.currentUser()).rejects.toMatchObject({ status: 401 });
    await expect(api.login('reader@example.com', 'wrong-password')).rejects.toMatchObject({ status: 401 });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('does not report other statuses as an expired session', async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ code: 'auth.administrator_required', error: 'No.' }, 403)));
    await expect(api.getAdminSettings()).rejects.toMatchObject({ status: 403 });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});

describe('authenticated API', () => {
  it.each(['null', '[]', '42', '{"error":{"message":"bad"},"code":42}', '<html>Bad Gateway</html>'])(
    'uses the status fallback for an unexpected error response: %s', async (body) => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { status: 502 })));
      await expect(api.currentUser()).rejects.toThrow(i18n.t('apiErrors.request_failed', { status: 502 }));
    },
  );

  it('preserves a server error message when its code is unknown', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ code: 'unknown_code', error: 'Please retry later.' }), { status: 503 })));
    await expect(api.currentUser()).rejects.toThrow('Please retry later.');
  });

  it.each(['allow', 'reject'] as const)('posts a quarantine %s decision with same-origin cookies', async (decision) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    if (decision === 'allow') await api.allowBlockedFeedArticle(7);
    else await api.rejectBlockedFeedArticle(7);
    expect(fetchMock).toHaveBeenCalledWith(`/api/admin/blocked-articles/7/${decision}`, expect.objectContaining({ method: 'POST', credentials: 'same-origin' }));
  });

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
