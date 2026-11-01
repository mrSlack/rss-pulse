import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from './AppChrome';

type Props = {
  icon: string;
  eyebrow: string;
  title: string;
  description: ReactNode;
  /** Short note at the bottom of the card. */
  footnote?: string;
  onClose: () => void;
  children: ReactNode;
};

/** The shared modal dialog card; a click on the backdrop closes it. */
export function ModalFrame({ icon, eyebrow, title, description, footnote, onClose, children }: Props) {
  const { t } = useTranslation();
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section className="modal-card" role="dialog" aria-modal="true" aria-labelledby="modal-title">
        <button className="icon-button modal-close" onClick={onClose} aria-label={t('actions.close')}><Icon name="close" size={19} /></button>
        <div className="modal-icon"><Icon name={icon} size={21} /></div>
        <p className="modal-eyebrow">{eyebrow}</p>
        <h2 id="modal-title">{title}</h2>
        <p className="modal-copy">{description}</p>
        {children}
        {footnote && <div className="modal-footnote"><span className="secure-dot" />{footnote}</div>}
      </section>
    </div>
  );
}
