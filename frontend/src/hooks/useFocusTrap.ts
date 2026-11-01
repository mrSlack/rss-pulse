import { useEffect, useEffectEvent, useState, type RefObject } from 'react';

const focusableSelector = 'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Keeps keyboard focus inside the dialog element while it is mounted: focuses
 * its first control, cycles Tab and Shift+Tab within it, closes it on Escape,
 * and returns focus to the previously focused element afterwards.
 */
export function useFocusTrap(dialogRef: RefObject<HTMLElement | null>, onClose: () => void) {
  // Captured during the first render, before the dialog takes focus.
  const [previouslyFocused] = useState(() => (document.activeElement instanceof HTMLElement ? document.activeElement : null));
  const close = useEffectEvent(onClose);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    const focusable = () => Array.from(dialog.querySelectorAll<HTMLElement>(focusableSelector));
    if (!dialog.contains(document.activeElement)) focusable()[0]?.focus();

    function handleKeys(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        event.preventDefault();
        close();
        return;
      }
      if (event.key !== 'Tab') return;
      const elements = focusable();
      const first = elements[0];
      const last = elements[elements.length - 1];
      const active = document.activeElement as HTMLElement | null;
      if (!first || !last) {
        event.preventDefault();
      } else if (event.shiftKey && (active === first || !dialog!.contains(active))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (active === last || !dialog!.contains(active))) {
        event.preventDefault();
        first.focus();
      }
    }
    document.addEventListener('keydown', handleKeys);
    return () => {
      document.removeEventListener('keydown', handleKeys);
      if (previouslyFocused?.isConnected) previouslyFocused.focus();
    };
  }, [dialogRef, previouslyFocused]);
}
