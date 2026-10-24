import type { Theme } from './theme';
import { useTranslation } from 'react-i18next';
import './i18n';

type ThemeToggleProps = {
  theme: Theme;
  onToggle: () => void;
};

export function ThemeToggle({ theme, onToggle }: ThemeToggleProps) {
  const { t } = useTranslation();
  const target = theme === 'dark' ? 'light' : 'dark';
  const action = target === 'light' ? t('theme.switchToLight') : t('theme.switchToDark');

  return (
    <button className="icon-button theme-toggle" type="button" aria-label={action} title={action} onClick={onToggle}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        {target === 'light' ? (
          <><circle cx="12" cy="12" r="4" /><path d="M12 2v2m0 16v2M4.93 4.93l1.42 1.42m11.3 11.3 1.42 1.42M2 12h2m16 0h2M4.93 19.07l1.42-1.42m11.3-11.3 1.42-1.42" /></>
        ) : (
          <path d="M20.9 13A8.5 8.5 0 0 1 11 3.1 8.6 8.6 0 1 0 20.9 13Z" />
        )}
      </svg>
    </button>
  );
}
