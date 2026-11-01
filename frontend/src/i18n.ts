import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from './locales/en.json';

export const supportedLanguages = ['en', 'de', 'ru'] as const;
export type Language = typeof supportedLanguages[number];

// English is bundled as the fallback; the other catalogs are separate chunks
// that are fetched only when a user picks (or had picked) that language.
const catalogLoaders: Record<Exclude<Language, 'en'>, () => Promise<{ default: Record<string, string> }>> = {
  de: () => import('./locales/de.json'),
  ru: () => import('./locales/ru.json'),
};

function isLanguage(value: string | null): value is Language {
  return value !== null && supportedLanguages.includes(value as Language);
}

function storedLanguage(): Language {
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
    resources: { en: { translation: en } },
    lng: 'en',
    fallbackLng: 'en',
    supportedLngs: supportedLanguages,
    keySeparator: false,
    interpolation: { escapeValue: false },
    returnNull: false,
  });

/** Loads the catalog for `language` if it is not loaded yet. */
async function loadCatalog(language: Language): Promise<void> {
  if (language === 'en' || i18n.hasResourceBundle(language, 'translation')) return;
  const catalog = await catalogLoaders[language]();
  i18n.addResourceBundle(language, 'translation', catalog.default, true, true);
}

/**
 * Switches the interface language, loading its catalog first. If the catalog
 * cannot be fetched (for example offline), the current language is kept.
 */
export async function setLanguage(language: Language): Promise<void> {
  try {
    await loadCatalog(language);
  } catch {
    return;
  }
  await i18n.changeLanguage(language);
}

function applyDocumentLanguage(language: string) {
  if (typeof document === 'undefined') return;
  document.documentElement.lang = isLanguage(language) ? language : 'en';
  document.title = i18n.t('brand.title');
}

function rememberLanguage(language: string) {
  applyDocumentLanguage(language);
  try {
    window.localStorage.setItem('pulse.language', isLanguage(language) ? language : 'en');
  } catch {
    // Language selection remains available for the current page when storage is disabled.
  }
}

/**
 * Resolves once the stored language is active (immediately for English). If
 * its catalog cannot be loaded, English is shown but the choice is kept.
 */
export const languageReady: Promise<void> = (() => {
  const initial = storedLanguage();
  const ready = initial === 'en' ? Promise.resolve() : setLanguage(initial);
  return ready.then(() => {
    applyDocumentLanguage(i18n.language);
    i18n.on('languageChanged', rememberLanguage);
  });
})();

export default i18n;
