import { useEffect, useRef, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import './i18n';

type ArticleDrawerProps = {
  children: ReactNode;
  onClose: () => void;
};

export function ArticleDrawer({ children, onClose }: ArticleDrawerProps) {
  const { t } = useTranslation();
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  if (previouslyFocused.current === null && document.activeElement instanceof HTMLElement) {
    previouslyFocused.current = document.activeElement;
  }

  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    closeButtonRef.current?.focus();

    function handleKeys(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        onCloseRef.current();
        return;
      }
      if (event.key !== 'Tab') return;
      const focusable = Array.from(document.querySelectorAll<HTMLElement>(
        '.article-drawer button:not([disabled]), .article-drawer a[href], .article-drawer input:not([disabled]), .article-drawer select:not([disabled]), .article-drawer textarea:not([disabled]), .article-drawer [tabindex]:not([tabindex="-1"])',
      ));
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (!first || !last) {
        event.preventDefault();
      } else if (event.shiftKey && (document.activeElement === first || !focusable.includes(document.activeElement as HTMLElement))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (document.activeElement === last || !focusable.includes(document.activeElement as HTMLElement))) {
        event.preventDefault();
        first.focus();
      }
    }
    window.addEventListener('keydown', handleKeys);
    return () => {
      window.removeEventListener('keydown', handleKeys);
      document.body.style.overflow = previousOverflow;
      if (previouslyFocused.current?.isConnected) previouslyFocused.current.focus();
    };
  }, []);

  return createPortal((
    <div className="article-drawer-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section className="article-drawer" role="dialog" aria-modal="true" aria-label={t('article.dialogLabel')}>
        <button ref={closeButtonRef} className="icon-button article-drawer-close" aria-label={t('article.close')} onClick={onClose}><svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
        {children}
      </section>
    </div>
  ), document.body);
}
