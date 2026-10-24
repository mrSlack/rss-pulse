import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from './locales/en.json';
import de from './locales/de.json';
import ru from './locales/ru.json';

export const supportedLanguages = ['en', 'de', 'ru'] as const;
export type Language = typeof supportedLanguages[number];

function isLanguage(value: string | null): value is Language {
  return value !== null && supportedLanguages.includes(value as Language);
}

function initialLanguage(): Language {
  try {
    const stored = window.localStorage.getItem('pulse.language');
    return isLanguage(stored) ? stored : 'en';
  } catch {
    return 'en';
  }
}

void i18n
  .use(initReactI18next)
  .init({
    resources: {
      en: { translation: en },
      de: { translation: de },
      ru: { translation: ru },
    },
    lng: initialLanguage(),
    fallbackLng: 'en',
    supportedLngs: supportedLanguages,
    keySeparator: false,
    interpolation: { escapeValue: false },
    returnNull: false,
  });

function updateDocumentLanguage(language: string) {
  const selected = isLanguage(language) ? language : 'en';
  if (typeof document !== 'undefined') document.documentElement.lang = selected;
  if (typeof document !== 'undefined') document.title = i18n.t('brand.title');
  try {
    window.localStorage.setItem('pulse.language', selected);
  } catch {
    // Language selection remains available for the current page when storage is disabled.
  }
}

i18n.on('languageChanged', updateDocumentLanguage);
updateDocumentLanguage(i18n.language);

export default i18n;
