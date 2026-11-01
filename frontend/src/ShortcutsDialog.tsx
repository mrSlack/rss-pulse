import { useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from './AppChrome';
import { shortcutKeys } from './hooks/useKeyboardShortcuts';
import { useFocusTrap } from './hooks/useFocusTrap';

/** Lists the reader's keyboard shortcuts. */
export function ShortcutsDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const dialogRef = useRef<HTMLElement>(null);
  useFocusTrap(dialogRef, onClose);

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section ref={dialogRef} className="modal-card shortcuts-card" role="dialog" aria-modal="true" aria-labelledby="shortcuts-title" aria-describedby="shortcuts-description">
        <button className="icon-button modal-close" onClick={onClose} aria-label={t('actions.close')}><Icon name="close" size={19} /></button>
        <div className="modal-icon"><Icon name="keyboard" size={21} /></div>
        <p className="modal-eyebrow">{t('reader.library')}</p>
        <h2 id="shortcuts-title">{t('shortcuts.title')}</h2>
        <p className="modal-copy" id="shortcuts-description">{t('shortcuts.description')}</p>
        <dl className="shortcut-list">
          {shortcutKeys.map(({ action, keys }) => (
            <div className="shortcut-row" key={action}>
              <dt>{keys.map((key, index) => <span key={key}>{index > 0 && <span className="shortcut-or">{t('shortcuts.or')}</span>}<kbd>{key}</kbd></span>)}</dt>
              <dd>{t(`shortcuts.action.${action}`)}</dd>
            </div>
          ))}
        </dl>
      </section>
    </div>
  );
}
