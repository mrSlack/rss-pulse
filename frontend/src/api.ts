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
};

export type AdminSettings = {
  refreshIntervalMinutes: number;
  sharedFeedTTLMinutes: number;
  users: SessionUser[];
};

type ApiError = { code?: string; error?: string };

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api${path}`, {
      ...options,
      credentials: 'same-origin',
      headers: {
        ...(options?.body ? { 'Content-Type': 'application/json' } : {}),
        ...options?.headers,
      },
    });
  } catch {
    throw new Error(i18n.t('apiErrors.network'));
  }

  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as ApiError;
    const fallback = body.error || i18n.t('apiErrors.request_failed', { status: response.status });
    const message = body.code
      ? i18n.t(`apiErrors.${body.code}`, { defaultValue: fallback, status: response.status })
      : fallback;
    throw new Error(message);
  }

  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export const api = {
  currentUser: () => request<SessionUser>('/auth/me'),
  login: (email: string, password: string) => request<SessionUser>('/auth/login', {
    method: 'POST',
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
  getFolders: () => request<Folder[]>('/folders'),
  getFeeds: () => request<Feed[]>('/feeds'),
  getArticles: (params: URLSearchParams) => request<ArticlePage>(`/articles?${params}`),
  createFeed: (url: string, folderId: number | null) =>
    request<Feed>('/feeds', {
      method: 'POST',
      body: JSON.stringify({ url, folderId }),
    }),
  updateFeed: (id: number, feed: { title: string; url: string; folderId: number | null }) =>
    request<Feed>(`/feeds/${id}`, {
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
  refreshStatus: () => request<{ running: boolean }>('/refresh'),
  getAdminSettings: () => request<AdminSettings>('/admin/settings'),
  updateAdminSettings: (settings: Pick<AdminSettings, 'refreshIntervalMinutes' | 'sharedFeedTTLMinutes'>) =>
    request<Pick<AdminSettings, 'refreshIntervalMinutes' | 'sharedFeedTTLMinutes'>>('/admin/settings', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),
  createUser: (user: { email: string; password: string; role: UserRole }) => request<SessionUser>('/admin/users', {
    method: 'POST',
    body: JSON.stringify(user),
  }),
  updateUser: (id: number, update: { role?: UserRole; refreshIntervalMinutes?: number | null }) =>
    request<SessionUser>(`/admin/users/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(update),
    }),
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
