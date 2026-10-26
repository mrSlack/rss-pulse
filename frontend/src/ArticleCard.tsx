import { useTranslation } from 'react-i18next';
import { stripMarkup, type Article } from './api';
import { ArticleTitle } from './ArticleTitle';
import { Icon, dateLabel, initials } from './AppChrome';

type Props = {
  article: Article;
  selected: boolean;
  locale: string;
  folderName: string | null;
  onChoose: () => void;
  onToggleRead: () => void;
  onToggleSaved: () => void;
};

export function ArticleCard({ article, selected, locale, folderName, onChoose, onToggleRead, onToggleSaved }: Props) {
  const { t } = useTranslation();
  const summary = stripMarkup(article.summary);

  return (
    <article className={`article-card ${selected ? 'selected' : ''} ${article.isRead ? 'read' : 'unread'}`}>
      <div
        className="article-card-main"
        role="group"
        tabIndex={0}
        aria-label={t('article.cardLabel', { title: article.title })}
        onClick={(event) => {
          if (!(event.target instanceof Element) || !event.target.closest('a, button')) {
            event.currentTarget.focus();
            onChoose();
          }
        }}
        onKeyDown={(event) => {
          if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) {
            event.preventDefault();
            onChoose();
          }
        }}
      >
        <div className="article-card-meta">
          {!article.isRead && <span className="article-unread-indicator" aria-hidden="true" />}
          <span className={`source-mark source-mark-${Math.abs(article.feedId) % 5}`}>{initials(article.feedTitle)}</span>
          <span className="source-title">{article.feedTitle}</span>
          <span className="meta-separator">·</span>
          <time>{dateLabel(article.publishedAt, locale)}</time>
        </div>
        <h2><ArticleTitle href={article.url} title={article.title} onOpenLocal={onChoose} /></h2>
        <p>{summary || t('article.summaryFallback')}</p>
        <div className="article-card-footer">
          <span className="article-topic">{folderName || t('reader.inbox')}</span>
          <span className="read-time">{t('reader.readTime', { count: Math.max(1, Math.ceil(summary.split(/\s+/).length / 220)) })}</span>
        </div>
      </div>
      <div className="article-actions">
        <button
          className={`read-state-button ${article.isRead ? 'marked-read' : ''}`}
          aria-label={article.isRead ? t('actions.markUnread') : t('actions.markRead')}
          title={article.isRead ? t('actions.markUnread') : t('actions.markRead')}
          onClick={onToggleRead}
        ><Icon name={article.isRead ? 'inbox' : 'check'} size={16} /></button>
        <button
          className={`save-button ${article.isSaved ? 'saved' : ''}`}
          aria-label={article.isSaved ? t('actions.removeSaved') : t('actions.saveForLater')}
          title={article.isSaved ? t('actions.removeSaved') : t('actions.saveForLater')}
          onClick={onToggleSaved}
        ><Icon name="bookmark" size={17} /></button>
      </div>
    </article>
  );
}
