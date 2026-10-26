import { useEffect, useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { SessionUser } from './api';

const iconShapes: Record<string, ReactNode> = {
  rss: <><path d="M5 19a1 1 0 1 0 0-2 1 1 0 0 0 0 2Z" /><path d="M4 11a9 9 0 0 1 9 9M4 5a15 15 0 0 1 15 15" /></>,
  inbox: <><path d="M4 5h16l1 10h-5l-2 3h-4l-2-3H3L4 5Z" /><path d="M3.5 13h5l1.5 2h3l1.5-2h5" /></>,
  list: <><path d="M9 6h11M9 12h11M9 18h11" /><path d="M4 6h.01M4 12h.01M4 18h.01" /></>,
  menu: <><path d="M4 7h16M4 12h16M4 17h16" /></>,
  clock: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></>,
  bookmark: <path d="M6 4.8A1.8 1.8 0 0 1 7.8 3h8.4A1.8 1.8 0 0 1 18 4.8V21l-6-3.8L6 21V4.8Z" />,
  plus: <><path d="M12 5v14M5 12h14" /></>,
  search: <><circle cx="10.8" cy="10.8" r="6.8" /><path d="m16 16 4.5 4.5" /></>,
  refresh: <><path d="M20 7v5h-5M4 17v-5h5" /><path d="M5.7 9A7 7 0 0 1 18 6.5L20 12M4 12l2 5.5A7 7 0 0 0 18.3 15" /></>,
  sparkle: <><path d="m12 3 1.7 5.3L19 10l-5.3 1.7L12 17l-1.7-5.3L5 10l5.3-1.7L12 3Z" /><path d="m19 15 .9 2.1L22 18l-2.1.9L19 21l-.9-2.1L16 18l2.1-.9L19 15Z" /></>,
  arrow: <><path d="M5 12h14M13 6l6 6-6 6" /></>,
  folder: <path d="M3 6.5A1.5 1.5 0 0 1 4.5 5h5l2 2H19a2 2 0 0 1 2 2v8.5a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5v-11Z" />,
  settings: <><circle cx="12" cy="12" r="3" /><path d="m19.4 15 .1.1a1.8 1.8 0 1 1-2.5 2.5l-.1-.1a1.8 1.8 0 0 0-3 .9v.2a1.8 1.8 0 1 1-3.6 0v-.2a1.8 1.8 0 0 0-3-.9l-.1.1a1.8 1.8 0 1 1-2.5-2.5l.1-.1a1.8 1.8 0 0 0-.9-3h-.2a1.8 1.8 0 1 1 0-3.6h.2a1.8 1.8 0 0 0 .9-3l-.1-.1a1.8 1.8 0 1 1 2.5-2.5l.1.1a1.8 1.8 0 0 0 3-.9v-.2a1.8 1.8 0 1 1 3.6 0v.2a1.8 1.8 0 0 0 3 .9l.1-.1a1.8 1.8 0 1 1 2.5 2.5l-.1.1a1.8 1.8 0 0 0 .9 3h.2a1.8 1.8 0 1 1 0 3.6h-.2a1.8 1.8 0 0 0-.9 3Z" /></>,
  trash: <><path d="M4 7h16M10 11v6M14 11v6M5 7l1 13h12l1-13M9 7V4h6v3" /></>,
  close: <><path d="m6 6 12 12M18 6 6 18" /></>,
  external: <><path d="M13 5h6v6M19 5l-9 9" /><path d="M18 13v5a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" /></>,
  check: <path d="m5 12 4 4L19 6" />,
  user: <><circle cx="12" cy="8" r="4" /><path d="M5 21a7 7 0 0 1 14 0" /></>,
  logout: <><path d="M10 17l5-5-5-5M15 12H3" /><path d="M12 3h6a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-6" /></>,
  edit: <><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L8 18l-4 1 1-4 11.5-11.5Z" /></>,
  chevron: <path d="m7 10 5 5 5-5" />,
};

export function Icon({ name, size = 18, className = '' }: { name: string; size?: number; className?: string }) {
  return (
    <svg className={className} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {iconShapes[name]}
    </svg>
  );
}

export function ProfileMenu({
  user,
  placement,
  adminActive,
  onAdmin,
  onEditProfile,
  onSignOut,
}: {
  user: SessionUser;
  placement: 'sidebar' | 'topbar';
  adminActive: boolean;
  onAdmin: () => void;
  onEditProfile: () => void;
  onSignOut: () => void;
}) {
  const { t } = useTranslation();
  const menuRef = useRef<HTMLDetailsElement>(null);
  const isAdministrator = user.role === 'Administrator';
  const roleLabel = isAdministrator ? t('roles.administrator') : t('roles.reader');

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

  function runAction(action: () => void) {
    if (menuRef.current) menuRef.current.open = false;
    action();
  }

  return (
    <details ref={menuRef} className={`profile-menu-container profile-menu-${placement}`}>
      <summary className={`profile-menu-trigger ${placement === 'sidebar' ? 'profile-row' : ''}`} aria-label={t('profile.menuFor', { email: user.email })}>
        <span className={`profile-avatar ${placement === 'topbar' ? 'topbar-avatar' : ''}`}>{initials(user.email)}</span>
        <span className="profile-copy"><strong>{user.email}</strong><small>{roleLabel}</small></span>
        <Icon name="chevron" size={16} className="profile-menu-chevron" />
      </summary>
      <div className="profile-dropdown" aria-label={t('profile.menu')}>
        <div className="profile-dropdown-heading">
          <span className="profile-dropdown-avatar">{initials(user.email)}</span>
          <span><strong>{user.email}</strong><small>{roleLabel}</small></span>
        </div>
        <div className="profile-dropdown-separator" />
        {isAdministrator && <button className={`profile-menu-item ${adminActive ? 'active' : ''}`} type="button" onClick={() => runAction(onAdmin)}>
          <Icon name="sparkle" size={17} /><span>{t('profile.administration')}</span>
        </button>}
        <button className="profile-menu-item" type="button" onClick={() => runAction(onEditProfile)}>
          <Icon name="user" size={17} /><span>{t('profile.edit')}</span>
        </button>
        <div className="profile-dropdown-separator" />
        <button className="profile-menu-item profile-menu-signout" type="button" onClick={() => runAction(onSignOut)}>
          <Icon name="logout" size={17} /><span>{t('profile.signOut')}</span>
        </button>
      </div>
    </details>
  );
}

export function initials(value: string): string {
  const parts = value.trim().split(/\s+/).slice(0, 2);
  return parts.map((part) => part[0]?.toUpperCase() || '').join('') || 'R';
}

export function dateLabel(value: string, locale: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const delta = Date.now() - date.getTime();
  if (delta < 60 * 60 * 1000) return new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(-Math.max(1, Math.floor(delta / 60000)), 'minute');
  if (delta < 24 * 60 * 60 * 1000) return new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(-Math.floor(delta / 3600000), 'hour');
  return new Intl.DateTimeFormat(locale, { day: 'numeric', month: 'short' }).format(date);
}

