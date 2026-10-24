import { createPortal } from 'react-dom';
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent } from 'react';
import { folderColorClass, folderColorStyle, type Folder, type FolderColor } from './api';

type FolderPickerProps = {
  id: string;
  labelId: string;
  folders: Folder[];
  value: string;
  noneLabel: string;
  onChange: (value: string) => void;
};

export function FolderPicker({ id, labelId, folders, value, noneLabel, onChange }: FolderPickerProps) {
  const listboxId = `${id}-listbox`;
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listboxRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const [position, setPosition] = useState<CSSProperties | null>(null);
  const options: { value: string; label: string; color?: FolderColor }[] = [{ value: '', label: noneLabel }, ...folders.map((folder) => ({ value: String(folder.id), label: folder.name, color: folder.color }))];
  const selectedIndex = Math.max(0, options.findIndex((option) => option.value === value));
  const selected = options[selectedIndex];

  const updatePosition = useCallback(() => {
    const trigger = triggerRef.current;
    if (!trigger) return;
    const rect = trigger.getBoundingClientRect();
    const naturalHeight = Math.min(options.length * 40 + 8, 240);
    const spaceBelow = Math.max(0, window.innerHeight - rect.bottom - 8);
    const spaceAbove = Math.max(0, rect.top - 8);
    const openAbove = spaceBelow < naturalHeight && spaceAbove > spaceBelow;
    const maxHeight = Math.max(1, Math.min(naturalHeight, openAbove ? spaceAbove : spaceBelow));
    const width = Math.min(rect.width, window.innerWidth - 16);
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8));
    setPosition({ left, top: openAbove ? rect.top - maxHeight - 6 : rect.bottom + 6, width, maxHeight });
  }, [options.length]);

  useLayoutEffect(() => {
    if (!open) return;
    updatePosition();
    window.addEventListener('resize', updatePosition);
    window.addEventListener('scroll', updatePosition, true);
    return () => {
      window.removeEventListener('resize', updatePosition);
      window.removeEventListener('scroll', updatePosition, true);
    };
  }, [open, updatePosition]);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && !triggerRef.current?.contains(target) && !listboxRef.current?.contains(target)) setOpen(false);
    };
    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener('pointerdown', closeOnOutsidePointer);
    document.addEventListener('keydown', closeOnEscape);
    return () => {
      document.removeEventListener('pointerdown', closeOnOutsidePointer);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [open]);

  function handleKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>) {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const direction = event.key === 'ArrowDown' ? 1 : -1;
      if (!open) {
        setActiveIndex(selectedIndex);
        setOpen(true);
      } else {
        setActiveIndex((current) => Math.max(0, Math.min(options.length - 1, current + direction)));
      }
      return;
    }
    if ((event.key === 'Enter' || event.key === ' ') && open) {
      event.preventDefault();
      onChange(options[activeIndex].value);
      setOpen(false);
    }
    if (event.key === 'Escape' && open) {
      event.preventDefault();
      setOpen(false);
    }
  }

  return <>
    <button
      ref={triggerRef}
      id={id}
      className="folder-picker-trigger"
      type="button"
      role="combobox"
      aria-labelledby={labelId}
      aria-haspopup="listbox"
      aria-expanded={open}
      aria-controls={listboxId}
      aria-activedescendant={open ? `${listboxId}-option-${activeIndex}` : undefined}
      onClick={() => {
        setActiveIndex(selectedIndex);
        setOpen((current) => !current);
      }}
      onKeyDown={handleKeyDown}
    >
      <span className="folder-picker-selected">{selected.color && <span aria-hidden="true" className={`folder-picker-color ${folderColorClass(selected.color)}`} style={folderColorStyle(selected.color)} />}{selected.label}</span>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m7 10 5 5 5-5" /></svg>
    </button>
    {open && position && createPortal(
      <div ref={listboxRef} id={listboxId} className="folder-picker-listbox" role="listbox" aria-labelledby={labelId} style={position}>
        {options.map((option, index) => (
          <div
            key={option.value || 'none'}
            id={`${listboxId}-option-${index}`}
            className={`folder-picker-option ${activeIndex === index ? 'active' : ''}`}
            role="option"
            aria-selected={value === option.value}
            onMouseEnter={() => setActiveIndex(index)}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              onChange(option.value);
              setOpen(false);
              triggerRef.current?.focus();
            }}
          ><span className="folder-picker-option-label">{option.color && <span aria-hidden="true" className={`folder-picker-color ${folderColorClass(option.color)}`} style={folderColorStyle(option.color)} />}{option.label}</span></div>
        ))}
      </div>,
      document.body,
    )}
  </>;
}
