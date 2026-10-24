import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';

type ErrorToastProps = {
  message: string;
  onDismiss: () => void;
};

export function ErrorToast({ message, onDismiss }: ErrorToastProps) {
  const { t } = useTranslation();
  if (!message) return null;

  return createPortal(
    <div className="error-toast" role="alert" aria-live="assertive" aria-atomic="true">
      <span className="error-toast-icon" aria-hidden="true">!</span>
      <span className="error-toast-message">{message}</span>
      <button className="error-toast-dismiss" type="button" onClick={onDismiss} aria-label={t('actions.dismiss')}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
          <path d="m6 6 12 12M18 6 6 18" />
        </svg>
      </button>
    </div>,
    document.body,
  );
}
