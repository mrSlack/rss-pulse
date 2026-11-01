import { useEffect, useEffectEvent } from 'react';

export type ShortcutAction =
  | 'next'
  | 'previous'
  | 'openOriginal'
  | 'toggleRead'
  | 'toggleSaved'
  | 'refresh'
  | 'focusSearch'
  | 'showHelp';

/** Keys of each action, in the order the help dialog lists them. */
export const shortcutKeys: Array<{ action: ShortcutAction; keys: string[] }> = [
  { action: 'next', keys: ['j'] },
  { action: 'previous', keys: ['k'] },
  { action: 'openOriginal', keys: ['o', 'Enter'] },
  { action: 'toggleRead', keys: ['m'] },
  { action: 'toggleSaved', keys: ['s'] },
  { action: 'refresh', keys: ['r'] },
  { action: 'focusSearch', keys: ['/'] },
  { action: 'showHelp', keys: ['?'] },
];

const actionByKey = new Map(shortcutKeys.flatMap(({ action, keys }) => keys.map((key) => [key, action] as const)));

/** Reports whether keys typed into target belong to a text field. */
function isEditable(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement;
}

/** Reports whether Enter on target already has a meaning (a control or a focusable card). */
function handlesEnter(target: EventTarget | null): boolean {
  return target instanceof Element && target !== document.body
    && Boolean(target.closest('a[href], button, summary, [role="button"], [role="menuitem"], [role="menuitemradio"], [role="option"], [tabindex]'));
}

/**
 * Maps single-key shortcuts to actions while enabled. Keys typed into text
 * fields and keys with Ctrl, Alt or Meta are ignored, so browser and system
 * shortcuts keep working.
 */
export function useKeyboardShortcuts(enabled: boolean, onAction: (action: ShortcutAction) => void) {
  const handleKey = useEffectEvent((event: KeyboardEvent) => {
    if (event.defaultPrevented || event.isComposing || event.ctrlKey || event.altKey || event.metaKey) return;
    if (isEditable(event.target)) return;
    const action = actionByKey.get(event.key);
    if (!action || (event.key === 'Enter' && handlesEnter(event.target))) return;
    event.preventDefault();
    onAction(action);
  });

  useEffect(() => {
    if (!enabled) return;
    const listener = (event: KeyboardEvent) => handleKey(event);
    window.addEventListener('keydown', listener);
    return () => window.removeEventListener('keydown', listener);
  }, [enabled]);
}
