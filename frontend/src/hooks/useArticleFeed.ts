import { useCallback, useEffect, useEffectEvent, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, errorMessage, isAbortError, type Article, type ArticleQuery, type MarkReadBound } from '../api';
import type { Filter } from '../LibrarySidebar';

export const articlePageSize = 20;

export type ArticleView = {
  filter: Filter;
  feedId: number | null;
  folderId: number | null;
  search: string;
};

function startOfToday(): string {
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth(), now.getDate()).toISOString();
}

function queryFor(view: ArticleView): ArticleQuery {
  return {
    filter: view.filter,
    feedId: view.feedId,
    folderId: view.folderId,
    q: view.search.trim(),
    since: view.filter === 'today' ? startOfToday() : null,
  };
}

function articleParams(query: ArticleQuery, before?: string): URLSearchParams {
  const params = new URLSearchParams({ filter: query.filter, limit: String(articlePageSize) });
  if (before) params.set('before', before);
  if (query.feedId !== null) params.set('feedId', String(query.feedId));
  if (query.folderId !== null) params.set('folderId', String(query.folderId));
  if (query.q) params.set('q', query.q);
  if (query.since) params.set('since', query.since);
  return params;
}

type Options = {
  onError: (message: string) => void;
  /** Called after read states changed on the server so counters can reload. */
  onCountsChanged: () => Promise<void>;
};

/**
 * The article list of the current view with keyset paging, the selected
 * article and read/saved actions. Changing the view cancels pending loads;
 * read and saved toggles update the loaded pages in place.
 */
export function useArticleFeed({ filter, feedId, folderId, search }: ArticleView, { onError, onCountsChanged }: Options) {
  const { t } = useTranslation();
  const [articles, setArticles] = useState<Article[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadMoreFailed, setLoadMoreFailed] = useState(false);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loadedQuery, setLoadedQuery] = useState<ArticleQuery | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [latestArticleId, setLatestArticleId] = useState<number | null>(null);
  const [version, setVersion] = useState(0);
  const loadMoreRef = useRef<AbortController | null>(null);
  // Every view change or reload starts a new generation. The cursor and query
  // belong to the generation whose first page was loaded; until the new one
  // has loaded (during the search debounce, say) they must not be paged.
  const generationRef = useRef(0);
  const loadedGenerationRef = useRef(-1);

  const reportLoadError = useEffectEvent((reason: unknown) => onError(errorMessage(reason, t('errors.articlesLoad'))));

  useEffect(() => {
    const controller = new AbortController();
    const generation = ++generationRef.current;
    const timer = window.setTimeout(() => {
      setLoading(true);
      const query = queryFor({ filter, feedId, folderId, search });
      api.getArticles(articleParams(query), controller.signal)
        .then((page) => {
          if (controller.signal.aborted) return;
          setArticles(page.items);
          setNextCursor(page.hasMore && page.nextCursor ? page.nextCursor : null);
          setLoadedQuery(query);
          setLatestArticleId(page.latestArticleId ?? null);
          loadedGenerationRef.current = generation;
          setLoadMoreFailed(false);
          setSelectedId((current) => page.items.some((item) => item.id === current) ? current : null);
          onError('');
          setLoading(false);
        })
        .catch((reason: unknown) => {
          if (controller.signal.aborted || isAbortError(reason)) return;
          reportLoadError(reason);
          setLoading(false);
        });
    }, search ? 220 : 0);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
      // A page requested for the previous view must not be appended.
      loadMoreRef.current?.abort();
      loadMoreRef.current = null;
    };
  }, [filter, feedId, folderId, search, version, onError]);

  const loadMore = useCallback(async () => {
    const generation = generationRef.current;
    if (loadMoreRef.current || !nextCursor || !loadedQuery || loadedGenerationRef.current !== generation) return;
    const controller = new AbortController();
    loadMoreRef.current = controller;
    setLoadingMore(true);
    setLoadMoreFailed(false);
    try {
      const page = await api.getArticles(articleParams(loadedQuery, nextCursor), controller.signal);
      if (controller.signal.aborted || generation !== generationRef.current) return;
      setArticles((current) => {
        const loaded = new Set(current.map((item) => item.id));
        return [...current, ...page.items.filter((item) => !loaded.has(item.id))];
      });
      setNextCursor(page.hasMore && page.nextCursor ? page.nextCursor : null);
      onError('');
    } catch (reason) {
      if (controller.signal.aborted || isAbortError(reason)) return;
      setLoadMoreFailed(true);
      onError(errorMessage(reason, t('errors.loadMore')));
    } finally {
      // A request cancelled by a view change was already detached.
      if (loadMoreRef.current === controller) loadMoreRef.current = null;
      if (loadMoreRef.current === null) setLoadingMore(false);
    }
  }, [loadedQuery, nextCursor, onError, t]);

  /** Loads the first page of the current view again. */
  const reload = useCallback(() => setVersion((current) => current + 1), []);

  const setReadLocally = useCallback((ids: Set<number>, isRead: boolean) => {
    setArticles((current) => current.map((item) => ids.has(item.id) ? { ...item, isRead } : item));
  }, []);

  const toggleRead = useCallback(async (article: Article) => {
    const isRead = !article.isRead;
    setReadLocally(new Set([article.id]), isRead);
    try {
      await api.updateArticle(article.id, { isRead });
    } catch (reason) {
      setReadLocally(new Set([article.id]), article.isRead);
      onError(errorMessage(reason, t('errors.articleStatus')));
      return;
    }
    // Only counters change; the loaded pages, cursor, and scroll position stay.
    await onCountsChanged();
  }, [onCountsChanged, onError, setReadLocally, t]);

  /** Opens an article in the reader and marks it read. */
  const chooseArticle = useCallback(async (article: Article) => {
    setSelectedId(article.id);
    if (!article.isRead) await toggleRead(article);
  }, [toggleRead]);

  const toggleSaved = useCallback(async (article: Article) => {
    const isSaved = !article.isSaved;
    setArticles((current) => current.map((item) => item.id === article.id ? { ...item, isSaved } : item));
    try {
      // Saved state does not affect any counter, so nothing is reloaded. An
      // unsaved article stays in the Saved view until the next explicit load.
      await api.updateArticle(article.id, { isSaved });
    } catch (reason) {
      onError(errorMessage(reason, t('errors.articleSave')));
      setArticles((current) => current.map((item) => item.id === article.id ? { ...item, isSaved: article.isSaved } : item));
    }
  }, [onError, t]);

  /**
   * Marks every article of the loaded view as read on the server, bounded by
   * the articles that existed when the list was loaded so articles that
   * arrived since stay unread (even with an older publication date), and
   * updates the loaded pages and counters without reloading the list.
   */
  const markAllRead = useCallback(async () => {
    if (!loadedQuery) return 0;
    let covers: (item: Article) => boolean;
    let bound: MarkReadBound;
    if (latestArticleId !== null) {
      const upToId = latestArticleId;
      bound = { upToId };
      covers = (item) => item.id <= upToId;
    } else {
      // Older servers: bound by the newest loaded publication time. Send the
      // server's own timestamp: it keeps sub-millisecond precision.
      const newest = articles.reduce<string | null>((best, item) => best === null || Date.parse(item.publishedAt) > Date.parse(best) ? item.publishedAt : best, null);
      const upTo = newest ?? new Date().toISOString();
      const limit = Date.parse(upTo);
      bound = { upTo };
      covers = (item) => Date.parse(item.publishedAt) <= limit;
    }
    const { marked } = await api.markArticlesRead(loadedQuery, bound);
    setArticles((current) => current.map((item) => !item.isRead && covers(item) ? { ...item, isRead: true } : item));
    await onCountsChanged();
    return marked;
  }, [articles, latestArticleId, loadedQuery, onCountsChanged]);

  /** Opens the next (delta 1) or previous (delta -1) article of the list. */
  const selectRelative = useCallback((delta: 1 | -1) => {
    if (articles.length === 0) return;
    const index = articles.findIndex((item) => item.id === selectedId);
    const next = index < 0 ? 0 : index + delta;
    if (next < 0 || next >= articles.length) return;
    void chooseArticle(articles[next]);
    if (next >= articles.length - 3 && nextCursor) void loadMore();
  }, [articles, chooseArticle, loadMore, nextCursor, selectedId]);

  const selectedArticle = useMemo(() => articles.find((item) => item.id === selectedId) || null, [articles, selectedId]);

  return {
    articles,
    loading,
    loadingMore,
    loadMoreFailed,
    hasMore: nextCursor !== null,
    loadedQuery,
    loadMore,
    reload,
    selectedId,
    setSelectedId,
    selectedArticle,
    chooseArticle,
    toggleRead,
    toggleSaved,
    markAllRead,
    selectRelative,
  };
}
