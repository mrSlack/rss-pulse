import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import type { Article, Feed } from './api';
import { Icon } from './AppChrome';
import { ArticleCard } from './ArticleCard';
import type { Filter } from './LibrarySidebar';

type Props = {
  filter: Filter;
  search: string;
  locale: string;
  feeds: Feed[];
  articles: Article[];
  selectedId: number | null;
  loading: boolean;
  hasMore: boolean;
  loadingMore: boolean;
  loadMoreFailed: boolean;
  onToggleUnreadFilter: () => void;
  onMarkAllRead: () => void;
  onAddFeed: () => void;
  onChoose: (article: Article) => void;
  onToggleRead: (article: Article) => void;
  onToggleSaved: (article: Article) => void;
  onLoadMore: () => void;
};

/** The article list with its toolbar; loads the next page near the end. */
export function ArticleListPane({
  filter, search, locale, feeds, articles, selectedId, loading, hasMore, loadingMore, loadMoreFailed,
  onToggleUnreadFilter, onMarkAllRead, onAddFeed, onChoose, onToggleRead, onToggleSaved, onLoadMore,
}: Props) {
  const { t } = useTranslation();
  const sentinelRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  // Unread articles may also exist beyond the loaded pages.
  const canMarkAllRead = hasMore || articles.some((article) => !article.isRead);

  useEffect(() => {
    // Keeps the article opened with the keyboard visible in the list.
    if (selectedId !== null) listRef.current?.querySelector('.article-card.selected')?.scrollIntoView?.({ block: 'nearest' });
  }, [selectedId]);

  useEffect(() => {
    const sentinel = sentinelRef.current;
    if (!sentinel || !hasMore || loadingMore || loadMoreFailed || loading) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) onLoadMore();
    }, { rootMargin: '300px 0px' });
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [hasMore, loadMoreFailed, loading, loadingMore, onLoadMore]);

  return (
    <section className="article-column" aria-label={t('reader.articleList')}>
      <div className="list-toolbar">
        <div className="list-toolbar-label"><span className="live-dot" />{filter === 'all' ? t('reader.yourFeed') : t(`filters.${filter}`)}<span className="toolbar-count">{articles.length}</span></div>
        <div className="list-toolbar-actions">
          <button className="sort-button mark-all-button" onClick={onMarkAllRead} disabled={!canMarkAllRead} aria-label={t('markAllRead.action')} title={t('markAllRead.action')}>
            <Icon name="check" size={14} />
            <span className="mark-all-label">{t('markAllRead.short')}</span>
          </button>
          <button className="sort-button" onClick={onToggleUnreadFilter} title={t('filters.toggleUnread')}>
            <span className={`unread-toggle ${filter === 'unread' ? 'checked' : ''}`}>{filter === 'unread' && <Icon name="check" size={11} />}</span>
            {t('filters.unread')}
          </button>
        </div>
      </div>

      <div className="article-list" ref={listRef}>
        {loading && articles.length === 0 ? <div className="list-loading"><span className="loader" />{t('states.loadingFeed')}</div> : null}
        {!loading && articles.length === 0 ? (
          <div className="empty-list">
            <span className="empty-icon"><Icon name={search ? 'search' : 'inbox'} size={22} /></span>
            <strong>{search ? t('states.noResults') : filter === 'saved' ? t('states.nothingSaved') : t('states.quiet')}</strong>
            <span>{search ? t('states.tryAnotherSearch') : t('states.addFeedHint')}</span>
            {!search && filter !== 'saved' && <button className="text-action" onClick={onAddFeed}>{t('actions.addSource')}<Icon name="arrow" size={15} /></button>}
          </div>
        ) : articles.map((article) => (
          <ArticleCard
            key={article.id}
            article={article}
            selected={selectedId === article.id}
            locale={locale}
            folderName={feeds.find((item) => item.id === article.feedId)?.folderName ?? null}
            onChoose={() => onChoose(article)}
            onToggleRead={() => onToggleRead(article)}
            onToggleSaved={() => onToggleSaved(article)}
          />
        ))}
      </div>
      {hasMore && <div className="load-more-wrap" ref={sentinelRef}>
        {loadingMore ? <span className="load-more-status" role="status"><span className="button-spinner" />{t('states.loading')}</span> : loadMoreFailed ? <button className="load-more-button" onClick={onLoadMore}>{t('actions.loadMore')}</button> : null}
      </div>}
      {articles.length > 0 && !hasMore && <div className="list-bottom-note"><span className="bottom-rule" />{t('states.endOfList')}<span className="bottom-rule" /></div>}
    </section>
  );
}
