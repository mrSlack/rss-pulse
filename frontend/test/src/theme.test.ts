import { describe, expect, it } from 'vitest';
import { readTheme, saveTheme } from '../../src/theme';

function storageWith(value: string | null): Storage {
  return {
    getItem: () => value,
    setItem: () => undefined,
    removeItem: () => undefined,
    clear: () => undefined,
    key: () => null,
    get length() { return value === null ? 0 : 1; },
  };
}

describe('theme preference', () => {
  it('uses a saved preference over the system preference', () => {
    expect(readTheme(storageWith('dark'), false)).toBe('dark');
    expect(readTheme(storageWith('light'), true)).toBe('light');
  });

  it('uses the system preference when no choice is saved', () => {
    expect(readTheme(storageWith(null), true)).toBe('dark');
    expect(readTheme(storageWith(null), false)).toBe('light');
  });

  it('ignores an invalid saved value', () => {
    expect(readTheme(storageWith('sepia'), true)).toBe('dark');
  });

  it('falls back to the system preference when storage reading fails', () => {
    const storage = storageWith(null);
    storage.getItem = () => { throw new Error('storage disabled'); };
    expect(readTheme(storage, true)).toBe('dark');
  });

  it('falls back to the system preference when storage is unavailable', () => {
    expect(readTheme(null, false)).toBe('light');
  });

  it('saves the selected theme under the pulse preference key', () => {
    let key = '';
    let value = '';
    const storage = storageWith(null);
    storage.setItem = (nextKey, nextValue) => { key = nextKey; value = nextValue; };

    saveTheme(storage, 'dark');

    expect(key).toBe('pulse-theme');
    expect(value).toBe('dark');
  });

  it('does not throw when storage saving fails or is unavailable', () => {
    const storage = storageWith(null);
    storage.setItem = () => { throw new Error('storage disabled'); };
    expect(() => saveTheme(storage, 'dark')).not.toThrow();
    expect(() => saveTheme(null, 'dark')).not.toThrow();
  });
});
