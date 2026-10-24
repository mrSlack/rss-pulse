export type Theme = 'light' | 'dark';

const THEME_STORAGE_KEY = 'pulse-theme';

export function readTheme(storage: Pick<Storage, 'getItem'> | null, prefersDark: boolean): Theme {
  try {
    const storedTheme = storage?.getItem(THEME_STORAGE_KEY);
    if (storedTheme === 'light' || storedTheme === 'dark') return storedTheme;
  } catch {
    // A browser may disable local storage; use the system preference instead.
  }

  return prefersDark ? 'dark' : 'light';
}

export function saveTheme(storage: Pick<Storage, 'setItem'> | null, theme: Theme): void {
  try {
    storage?.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // Keeping the current theme active matters more than persisting it.
  }
}
