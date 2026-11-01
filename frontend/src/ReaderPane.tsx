import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { extractArticleImages, stripMarkup, type Article } from './api';
import { Icon, dateLabel, initials } from './AppChrome';
import { ArticleDrawer } from './ArticleDrawer';

type Props = {
  article: Article;
  locale: string;
  onClose: () => void;
  onToggleRead: () => void;
  onToggleSaved: () => void;
};

/** The selected article in the reading drawer. */
export function ReaderPane({ article, locale, onClose, onToggleRead, onToggleSaved }: Props) {
  const { t } = useTranslation();
  const images = useMemo(() => extractArticleImages(article.summary, article.url), [article.summary, article.url]);
  const readLabel = article.isRead ? t('actions.markUnread') : t('actions.markRead');

  return (
    <ArticleDrawer onClose={onClose}>
      {/* Keyed by article so moving to another article starts at the top. */}
      <section key={article.id} className="reading-pane" aria-label={t('article.selected')}>
        <div className="reading-scroll">
          <div className="reading-toolbar"><span className="reading-source"><span className={`source-mark source-mark-${Math.abs(article.feedId) % 5}`}>{initials(article.feedTitle)}</span>{article.feedTitle}</span><div className="reading-actions"><button className="icon-button" aria-label={readLabel} title={readLabel} onClick={onToggleRead}><Icon name={article.isRead ? 'inbox' : 'check'} size={17} /></button><button className={`icon-button ${article.isSaved ? 'active-save' : ''}`} aria-label={t('actions.saveArticle')} title={t('actions.saveForLater')} onClick={onToggleSaved}><Icon name="bookmark" size={18} /></button>{article.url && <a className="icon-button" href={article.url} target="_blank" rel="noreferrer" aria-label={t('actions.openOriginal')} title={t('actions.openOriginal')}><Icon name="external" size={17} /></a>}</div></div>
          <div className="reading-article">
            <div className="reading-kicker"><span className="kicker-line" />{article.publishedAt ? new Intl.DateTimeFormat(locale, { dateStyle: 'long' }).format(new Date(article.publishedAt)) : t('article.new')}</div>
            <h2>{article.title}</h2>
            <div className="reading-byline"><span className="byline-avatar">{initials(article.feedTitle)}</span><span><strong>{article.feedTitle}</strong><small>{t('article.feedSource')}</small></span><span className="byline-dot">·</span><span className="reading-time">{dateLabel(article.publishedAt, locale)}</span></div>
            {images.length > 0 && <div className="reading-images" role="group" aria-label={t('article.images')}>{images.map((image) => <img key={image.src} src={image.src} alt={image.alt || article.title} loading="lazy" referrerPolicy="no-referrer" />)}</div>}
            <div className="reading-divider" />
            <p className="reading-lead">{stripMarkup(article.summary) || t('article.fullVersionFallback')}</p>
            <div className="source-note"><Icon name="sparkle" size={17} /><span>{t('article.sourceNote')}</span></div>
            {article.url && <a className="read-original" href={article.url} target="_blank" rel="noreferrer">{t('actions.readOriginal')} <Icon name="arrow" size={16} /></a>}
          </div>
          <div className="reader-footer"><span>{t('article.end')}</span><span className="footer-line" /></div>
        </div>
      </section>
    </ArticleDrawer>
  );
}
