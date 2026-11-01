import { afterEach, describe, expect, it } from 'vitest';
import i18n, { languageReady, setLanguage } from '../../src/i18n';

afterEach(async () => {
  await i18n.changeLanguage('en');
});

describe('lazily loaded translations', () => {
  it('bundles only English until another language is chosen', async () => {
    await languageReady;
    expect(i18n.language).toBe('en');
    expect(i18n.hasResourceBundle('en', 'translation')).toBe(true);
    expect(i18n.hasResourceBundle('de', 'translation')).toBe(false);
    expect(i18n.hasResourceBundle('ru', 'translation')).toBe(false);
  });

  it('loads a catalog before switching to it and remembers the choice', async () => {
    await languageReady;
    await setLanguage('de');
    expect(i18n.hasResourceBundle('de', 'translation')).toBe(true);
    expect(i18n.language).toBe('de');
    expect(i18n.t('actions.refresh')).not.toBe(i18n.t('actions.refresh', { lng: 'en' }));
    expect(document.documentElement.lang).toBe('de');
    expect(window.localStorage.getItem('pulse.language')).toBe('de');
  });
});
