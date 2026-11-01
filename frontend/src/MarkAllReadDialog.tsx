import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { errorMessage } from './api';
import { Icon } from './AppChrome';
import { ErrorToast } from './ErrorToast';
import { ModalFrame } from './ModalFrame';

type Props = {
  viewTitle: string;
  onConfirm: () => Promise<unknown>;
  onClose: () => void;
};

/** Confirms marking a view as read when it may contain many articles. */
export function MarkAllReadDialog({ viewTitle, onConfirm, onClose }: Props) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function confirm() {
    setBusy(true);
    setError('');
    try {
      await onConfirm();
      onClose();
    } catch (reason) {
      setError(errorMessage(reason, t('errors.markAllRead')));
      setBusy(false);
    }
  }

  return (
    <ModalFrame icon="check" eyebrow={t('reader.library')} title={t('markAllRead.confirmTitle')} description={t('markAllRead.confirmDescription', { view: viewTitle })} onClose={onClose}>
      <ErrorToast message={error} onDismiss={() => setError('')} />
      <button className="primary-button modal-submit" type="button" autoFocus disabled={busy} onClick={() => void confirm()}>{busy ? <><span className="button-spinner" />{t('states.saving')}</> : <>{t('markAllRead.confirm')}<Icon name="check" size={16} /></>}</button>
    </ModalFrame>
  );
}
