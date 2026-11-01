import i18n from './i18n';

export const folderColors = ['amber', 'blue', 'green', 'rose', 'violet', 'teal'] as const;
export type FolderColor = string;

export function folderColorClass(color: FolderColor): string {
  return folderColors.includes(color as typeof folderColors[number]) ? `folder-color-${color}` : 'folder-color-custom';
}

export function folderColorStyle(color: FolderColor) {
  return /^#[0-9a-fA-F]{6}$/.test(color) ? { backgroundColor: color } : undefined;
}

export type Folder = {
  id: number;
  name: string;
  color: FolderColor;
  unreadCount: number;
};

export type Feed = {
  id: number;
  title: string;
  url: string;
  folderId: number | null;
  folderName: string | null;
  unreadCount: number;
  lastCheckedAt: string | null;
  lastError: string | null;
};

export type Article = {
  id: number;
  feedId: number;
  feedTitle: string;
  feedUrl: string;
  title: string;
  url: string;
  summary: string;
  publishedAt: string;
  isRead: boolean;
  isSaved: boolean;
};

export type ArticlePage = {
  items: Article[];
  hasMore: boolean;
  /** Opaque keyset cursor for the next page; sent back as `before`. */
  nextCursor: string | null;
};

export type UserRole = 'Administrator' | 'Reader';

export type SessionUser = {
  id: number;
  email: string;
  role: UserRole;
  refreshIntervalMinutes: number | null;
  firstName?: string;
  lastName?: string;
  nickname?: string;
  /** Present in administrator user listings; disabled accounts cannot sign in. */
  disabled?: boolean;
  disabledAt?: string | null;
};

export type AdminSettings = {
  refreshIntervalMinutes: number;
  sharedFeedTTLMinutes: number;
  users: SessionUser[];
};

export type FeedRequest = {
  id: number;
  url: string;
  /** First requester; kept for compatibility with older responses. */
  requestedBy: string;
  /** Every requester's e-mail address in request order. */
  requesters?: string[];
  folderId: number | null;
  folderName: string | null;
  title: string;
  createdAt: string;
};

export type FeedRequestResult = { requestId: number; status: 'pending' };

export type StopWord = { id: number; term: string; createdAt: string };
export type QuarantineStatus = 'pending' | 'rejected' | 'allowed';
export type BlockedFeedArticle = {
  id: number;
  articleTitle: string;
  articleUrl: string;
  feedTitle: string;
  feedUrl: string;
  addedBy: string;
  matchedTerm: string;
  articleGuid: string;
  summary: string;
  publishedAt: string | null;
  status: QuarantineStatus;
  decidedAt: string | null;
  canModerate: boolean;
  firstBlockedAt: string;
  lastBlockedAt: string;
};


/** An API request that failed with an HTTP error status or a network error (status 0). */
export class ApiError extends Error {
  readonly status: number;
  /** Stable dotted error code from the server, or `network` for connection failures. */
  readonly code: string | undefined;

  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

/** Reports whether a request failed because its AbortSignal was aborted. */
export function isAbortError(reason: unknown): boolean {
  return reason instanceof DOMException && reason.name === 'AbortError';
}

/** Returns the message to show for a failed request, or `fallback` for unknown failures. */
export function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

let unauthorizedHandler: (() => void) | null = null;

/**
 * Registers the callback run when an authenticated request returns 401 (the
 * session expired or was revoked). The session probe and sign-in are exempt.
 */
export function setUnauthorizedHandler(handler: (() => void) | null) {
  unauthorizedHandler = handler;
}

function parseApiError(value: unknown): { code?: string; error?: string } {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return {};
  const body = value as Record<string, unknown>;
  return {
    code: typeof body.code === 'string' ? body.code : undefined,
    error: typeof body.error === 'string' ? body.error : undefined,
  };
}

type RequestOptions = RequestInit & {
  /** Skip the expired-session handler (session probe and sign-in). */
  anonymous?: boolean;
};

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { anonymous, ...init } = options;
  let response: Response;
  try {
    response = await fetch(`/api${path}`, {
      ...init,
      credentials: 'same-origin',
      headers: {
        ...(typeof init.body === 'string' ? { 'Content-Type': 'application/json' } : {}),
        ...init.headers,
      },
    });
  } catch (reason) {
    if (isAbortError(reason)) throw reason;
    throw new ApiError(i18n.t('apiErrors.network'), 0, 'network');
  }

  if (!response.ok) {
    const body = parseApiError(await response.json().catch(() => null));
    const fallback = body.error || i18n.t('apiErrors.request_failed', { status: response.status });
    const message = body.code
      ? i18n.t(`apiErrors.${body.code}`, { defaultValue: fallback, status: response.status })
      : fallback;
    if (response.status === 401 && !anonymous) unauthorizedHandler?.();
    throw new ApiError(message, response.status, body.code);
  }

  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

/** The view the article list shows; also the scope of "mark all as read". */
export type ArticleQuery = {
  filter: 'all' | 'unread' | 'today' | 'saved';
  feedId: number | null;
  folderId: number | null;
  q: string;
  /** Start of the Today filter (local midnight), RFC 3339. */
  since: string | null;
};

export type OpmlImportResult = {
  subscribed: number;
  requested: number;
  skipped: number;
  failed: Array<{ url: string; reason: string }>;
};

export const api = {
  currentUser: (signal?: AbortSignal) => request<SessionUser>('/auth/me', { anonymous: true, signal }),
  login: (email: string, password: string) => request<SessionUser>('/auth/login', {
    method: 'POST',
    anonymous: true,
    body: JSON.stringify({ email, password }),
  }),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
  changePassword: (currentPassword: string, newPassword: string) => request<void>('/auth/password', {
    method: 'POST',
    body: JSON.stringify({ currentPassword, newPassword }),
  }),
  updateProfile: (profile: { firstName: string; lastName: string; nickname: string }) => request<SessionUser>('/auth/profile', {
    method: 'PUT',
    body: JSON.stringify(profile),
  }),
  getFolders: (signal?: AbortSignal) => request<Folder[]>('/folders', { signal }),
  getFeeds: (signal?: AbortSignal) => request<Feed[]>('/feeds', { signal }),
  getArticles: (params: URLSearchParams, signal?: AbortSignal) => request<ArticlePage>(`/articles?${params}`, { signal }),
  markArticlesRead: (query: ArticleQuery, upTo: string, signal?: AbortSignal) => request<{ marked: number }>('/articles/mark-read', {
    method: 'POST',
    signal,
    body: JSON.stringify({
      filter: query.filter,
      ...(query.feedId !== null ? { feedId: query.feedId } : {}),
      ...(query.folderId !== null ? { folderId: query.folderId } : {}),
      ...(query.q ? { q: query.q } : {}),
      ...(query.since ? { since: query.since } : {}),
      upTo,
    }),
  }),
  importOpml: (document: Blob | string, signal?: AbortSignal) => request<OpmlImportResult>('/opml', {
    method: 'POST',
    signal,
    headers: { 'Content-Type': 'text/x-opml' },
    body: document,
  }),
  createFeed: (url: string, folderId: number | null) =>
    request<Feed | FeedRequestResult>('/feeds', {
      method: 'POST',
      body: JSON.stringify({ url, folderId }),
    }),
  updateFeed: (id: number, feed: { title: string; url: string; folderId: number | null }) =>
    request<Feed | FeedRequestResult>(`/feeds/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(feed),
    }),
  createFolder: (name: string, color: FolderColor) =>
    request<Folder>('/folders', {
      method: 'POST',
      body: JSON.stringify({ name, color }),
    }),
  updateFolder: (id: number, folder: { name: string; color: FolderColor }) =>
    request<Folder>(`/folders/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(folder),
    }),
  deleteFolder: (id: number, operation: { action: 'delete_sources' } | { action: 'move_sources'; targetFolderId: number }) =>
    request<void>(`/folders/${id}`, {
      method: 'DELETE',
      body: JSON.stringify(operation),
    }),
  updateArticle: (id: number, update: { isRead?: boolean; isSaved?: boolean }) =>
    request<Article>(`/articles/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(update),
    }),
  refresh: () => request<{ message: string }>('/refresh', { method: 'POST' }),
  refreshStatus: (signal?: AbortSignal) => request<{ running: boolean }>('/refresh', { signal }),
  getAdminSettings: (signal?: AbortSignal) => request<AdminSettings>('/admin/settings', { signal }),
  updateAdminSettings: (settings: Pick<AdminSettings, 'refreshIntervalMinutes' | 'sharedFeedTTLMinutes'>) =>
    request<Pick<AdminSettings, 'refreshIntervalMinutes' | 'sharedFeedTTLMinutes'>>('/admin/settings', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),
  createUser: (user: { email: string; password: string; role: UserRole }) => request<SessionUser>('/admin/users', {
    method: 'POST',
    body: JSON.stringify(user),
  }),
  updateUser: (id: number, update: { role?: UserRole; refreshIntervalMinutes?: number | null; disabled?: boolean }) =>
    request<SessionUser>(`/admin/users/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(update),
    }),
  signOutUser: (id: number) => request<void>(`/admin/users/${id}/sign-out`, { method: 'POST' }),
  getFeedRequests: (signal?: AbortSignal) => request<FeedRequest[]>('/admin/feed-requests', { signal }),
  approveFeedRequest: (id: number) => request<void>(`/admin/feed-requests/${id}/approve`, { method: 'POST', body: JSON.stringify({}) }),
  rejectFeedRequest: (id: number) => request<void>(`/admin/feed-requests/${id}`, { method: 'DELETE' }),
  getStopWords: (signal?: AbortSignal) => request<StopWord[]>('/admin/stop-words', { signal }),
  addStopWord: (term: string) => request<StopWord>('/admin/stop-words', { method: 'POST', body: JSON.stringify({ term }) }),
  deleteStopWord: (id: number) => request<void>(`/admin/stop-words/${id}`, { method: 'DELETE' }),
  getBlockedFeedArticles: (signal?: AbortSignal) => request<BlockedFeedArticle[]>('/admin/blocked-articles?limit=100', { signal }),
  allowBlockedFeedArticle: (id: number) => request<void>(`/admin/blocked-articles/${id}/allow`, { method: 'POST' }),
  rejectBlockedFeedArticle: (id: number) => request<void>(`/admin/blocked-articles/${id}/reject`, { method: 'POST' }),
};

export function stripMarkup(value: string): string {
  if (!value) return '';
  const parsed = new DOMParser().parseFromString(value, 'text/html');
  return (parsed.body.textContent || '').replace(/\s+/g, ' ').trim();
}

export type ArticleImage = { src: string; alt: string };

export function extractArticleImages(markup: string, articleUrl: string): ArticleImage[] {
  let base: URL;
  try {
    base = new URL(articleUrl);
    if (base.protocol !== 'http:' && base.protocol !== 'https:') return [];
  } catch {
    return [];
  }

  const parsed = new DOMParser().parseFromString(markup || '', 'text/html');
  const images = new Map<string, ArticleImage>();
  for (const element of Array.from(parsed.querySelectorAll('img, source'))) {
    const primarySource = ['src', 'data-src', 'data-original', 'data-lazy-src']
      .map((attribute) => element.getAttribute(attribute)?.trim() || '')
      .find(Boolean);
    const srcset = element.getAttribute('srcset') || element.getAttribute('data-srcset') || '';
    const candidates = primarySource
      ? [primarySource]
      : srcset.split(',').map((candidate) => candidate.trim().split(/\s+/, 1)[0]).filter(Boolean).slice(-1);
    const alt = element.tagName.toLowerCase() === 'img' ? element.getAttribute('alt')?.trim() || '' : '';

    for (const candidate of candidates) {
      try {
        const resolved = new URL(candidate, base);
        if (!['http:', 'https:'].includes(resolved.protocol) || resolved.username || resolved.password) continue;
        if (!images.has(resolved.href) || alt) images.set(resolved.href, { src: resolved.href, alt });
      } catch {
        // Ignore malformed source attributes from the feed.
      }
    }
  }
  return Array.from(images.values()).slice(0, 8);
}
