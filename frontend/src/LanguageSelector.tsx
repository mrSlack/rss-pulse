import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import i18n, { supportedLanguages, type Language } from './i18n';

const languageNames: Record<Language, string> = {
  en: 'English',
  de: 'Deutsch',
  ru: 'Русский',
};

export function LanguageSelector({ className = '' }: { className?: string }) {
  const { t } = useTranslation();
  const menuRef = useRef<HTMLDetailsElement>(null);
  const currentLanguage = supportedLanguages.includes(i18n.resolvedLanguage as Language)
    ? i18n.resolvedLanguage as Language
    : 'en';

  useEffect(() => {
    const menu = menuRef.current;
    if (!menu) return;
    const closeOnOutsideClick = (event: MouseEvent) => {
      if (event.target instanceof Node && !menu.contains(event.target)) menu.open = false;
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && menu.open) {
        menu.open = false;
        menu.querySelector('summary')?.focus();
      }
    };
    document.addEventListener('click', closeOnOutsideClick);
    document.addEventListener('keydown', closeOnEscape);
    return () => {
      document.removeEventListener('click', closeOnOutsideClick);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, []);

  function selectLanguage(language: Language) {
    void i18n.changeLanguage(language);
    if (menuRef.current) menuRef.current.open = false;
    menuRef.current?.querySelector('summary')?.focus();
  }

  return (
    <details ref={menuRef} className={`language-selector ${className}`.trim()}>
      <summary role="button" aria-label={t('language.label')} title={t('language.label')}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M3 12h18M12 3a15 15 0 0 1 0 18M12 3a15 15 0 0 0 0 18" />
        </svg>
      </summary>
      <div className="language-menu" role="menu" aria-label={t('language.label')}>
        {supportedLanguages.map((language) => (
          <button key={language} type="button" role="menuitemradio" aria-checked={currentLanguage === language} onClick={() => selectLanguage(language)}>
            <span>{languageNames[language]}</span>
            {currentLanguage === language && <span className="language-selected-mark" aria-hidden="true">✓</span>}
          </button>
        ))}
      </div>
    </details>
  );
}
