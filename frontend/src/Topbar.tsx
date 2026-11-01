import { useEffect, useImperativeHandle, useRef, useState, type Ref } from 'react';
import { useTranslation } from 'react-i18next';
import type { SessionUser } from './api';
import { Icon, ProfileMenu } from './AppChrome';
import { LanguageSelector } from './LanguageSelector';
import type { Theme } from './theme';
import { ThemeToggle } from './ThemeToggle';

export type TopbarHandle = {
  /** Focuses the visible search field (opening the mobile search if needed). */
  focusSearch: () => void;
};

type Props = {
  ref?: Ref<TopbarHandle>;
  user: SessionUser;
  search: string;
  onSearchChange: (value: string) => void;
  theme: Theme;
  onToggleTheme: () => void;
  refreshing: boolean;
  onRefresh: () => void;
  mobileNavOpen: boolean;
  onToggleMobileNav: () => void;
  adminActive: boolean;
  onAdmin: () => void;
  onEditProfile: () => void;
  onSignOut: () => void;
};

export function Topbar({
  ref, user, search, onSearchChange, theme, onToggleTheme, refreshing, onRefresh, mobileNavOpen, onToggleMobileNav,
  adminActive, onAdmin, onEditProfile, onSignOut,
}: Props) {
  const { t } = useTranslation();
  const [mobileSearchOpen, setMobileSearchOpen] = useState(false);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const mobileSearchInputRef = useRef<HTMLInputElement>(null);
  const mobileSearchButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (mobileSearchOpen) mobileSearchInputRef.current?.focus();
  }, [mobileSearchOpen]);

  useImperativeHandle(ref, () => ({
    focusSearch() {
      const input = searchInputRef.current;
      // The desktop field is hidden on narrow screens.
      if (input && input.getClientRects().length > 0) input.focus();
      else if (mobileSearchInputRef.current) mobileSearchInputRef.current.focus();
      else setMobileSearchOpen(true);
    },
  }), []);

  function closeMobileSearch() {
    setMobileSearchOpen(false);
    mobileSearchButtonRef.current?.focus();
  }

  return (
    <header className="topbar">
      <div className="mobile-brand">
        <button className="icon-button mobile-menu" aria-label={t('nav.openMenu')} aria-expanded={mobileNavOpen} onClick={onToggleMobileNav}><Icon name="menu" size={19} /></button>
        <span className="brand-mark"><Icon name="rss" size={19} /></span><span className="brand-name">Pulse<span>.</span></span>
      </div>
      <div className="topbar-search">
        <Icon name="search" size={18} />
        <input ref={searchInputRef} aria-label={t('search.label')} value={search} onChange={(event) => onSearchChange(event.target.value)} placeholder={t('search.placeholder')} />
      </div>
      <button ref={mobileSearchButtonRef} className="icon-button mobile-search-trigger" aria-label={t('search.label')} aria-expanded={mobileSearchOpen} onClick={() => setMobileSearchOpen(true)}><Icon name="search" size={19} /></button>
      {mobileSearchOpen && <div className="mobile-search-panel">
        <Icon name="search" size={19} />
        <input ref={mobileSearchInputRef} aria-label={t('search.label')} value={search} onChange={(event) => onSearchChange(event.target.value)} placeholder={t('search.placeholder')} onKeyDown={(event) => { if (event.key === 'Escape') closeMobileSearch(); }} />
        <button className="icon-button" aria-label={t('actions.close')} onClick={closeMobileSearch}><Icon name="close" size={17} /></button>
      </div>}
      <div className="topbar-actions">
        <ThemeToggle theme={theme} onToggle={onToggleTheme} />
        <LanguageSelector />
        <button className={`icon-button refresh-button ${refreshing ? 'spinning' : ''}`} aria-label={t('actions.refresh')} title={t('actions.refresh')} onClick={onRefresh} disabled={refreshing}>
          <Icon name="refresh" size={18} />
        </button>
        <span className="topbar-divider" />
        <ProfileMenu user={user} placement="topbar" adminActive={adminActive} onAdmin={onAdmin} onEditProfile={onEditProfile} onSignOut={onSignOut} />
      </div>
    </header>
  );
}
