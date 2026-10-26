import { useTranslation } from 'react-i18next';
import type { Feed, Folder, SessionUser } from './api';
import { Icon, ProfileMenu } from './AppChrome';

export type Filter = 'all' | 'unread' | 'today' | 'saved';

const filters: { id: Filter; icon: string }[] = [
  { id: 'all', icon: 'inbox' },
  { id: 'today', icon: 'clock' },
  { id: 'saved', icon: 'bookmark' },
];

type Props = {
  user: SessionUser;
  feeds: Feed[];
  folders: Folder[];
  filter: Filter;
  feedId: number | null;
  folderId: number | null;
  unreadTotal: number;
  adminView: boolean;
  mobileNavOpen: boolean;
  collapsedFolderIds: Set<number>;
  unfiledCollapsed: boolean;
  onHome: () => void;
  onAddFeed: () => void;
  onCreateFolder: () => void;
  onManageFolders: () => void;
  onSelectFilter: (filter: Filter) => void;
  onSelectFeed: (id: number) => void;
  onSelectFolder: (id: number) => void;
  onToggleFolder: (id: number) => void;
  onToggleUnfiled: () => void;
  onEditFeed: (feed: Feed) => void;
  onAdmin: () => void;
  onEditProfile: () => void;
  onSignOut: () => void;
  onCloseMobileNav: () => void;
};

export function LibrarySidebar({
  user,
  feeds,
  folders,
  filter,
  feedId,
  folderId,
  unreadTotal,
  adminView,
  mobileNavOpen,
  collapsedFolderIds,
  unfiledCollapsed,
  onHome,
  onAddFeed,
  onCreateFolder,
  onManageFolders,
  onSelectFilter,
  onSelectFeed,
  onSelectFolder,
  onToggleFolder,
  onToggleUnfiled,
  onEditFeed,
  onAdmin,
  onEditProfile,
  onSignOut,
  onCloseMobileNav,
}: Props) {
  const { t } = useTranslation();
  const visibleFolders = folders.filter((folder) => feeds.some((feed) => feed.folderId === folder.id));

  function renderFeedRow(feed: Feed) {
    return <div key={feed.id} className="feed-row tree-feed-row">
      <button className={`nav-item feed-item ${feedId === feed.id ? 'active' : ''}`} onClick={() => onSelectFeed(feed.id)} title={feed.title}>
        <span className="feed-name">{feed.title}</span>
        {feed.unreadCount > 0 && <span className="feed-unread">{feed.unreadCount}</span>}
      </button>
      <button className="feed-edit-button" type="button" onClick={() => onEditFeed(feed)} aria-label={t('feedModal.editFor', { title: feed.title })} title={t('actions.editFeed')}>
        <Icon name="edit" size={15} />
      </button>
    </div>;
  }

  return <>
    <aside className={`sidebar ${mobileNavOpen ? 'mobile-open' : ''}`}>
      <a className="brand" href="#home" onClick={(event) => { event.preventDefault(); onHome(); }} aria-label={t('brand.home')}>
        <span className="brand-mark"><Icon name="rss" size={22} /></span>
        <span className="brand-name">RSS-Pulse<span>.</span></span>
      </a>

      <button className="add-feed-button" onClick={onAddFeed}>
        <Icon name="plus" size={17} />
        <span>{t('actions.addSource')}</span>
      </button>

      <div className="sidebar-label">{t('nav.read')}</div>
      <nav className="primary-nav" aria-label={t('nav.primary')}>
        {filters.filter((item) => item.id !== 'all').map((item) => (
          <button
            key={item.id}
            className={`nav-item ${(filter === item.id && feedId === null && folderId === null) ? 'active' : ''}`}
            onClick={() => onSelectFilter(item.id)}
          >
            <Icon name={item.icon} size={18} />
            <span>{t(`filters.${item.id}`)}</span>
          </button>
        ))}
      </nav>

      <div className="sidebar-section-heading source-heading">
        <span className="sidebar-label">{t('nav.feedTree')}</span>
        <div className="library-heading-actions">
          <button className="icon-button tiny" title={t('actions.createFolder')} aria-label={t('actions.createFolder')} onClick={onCreateFolder}>
            <Icon name="folder" size={14} />
          </button>
          <button className="icon-button tiny" title={t('folder.manage')} aria-label={t('folder.manage')} onClick={onManageFolders}>
            <Icon name="settings" size={14} />
          </button>
          <button className="icon-button tiny" title={t('actions.addSource')} aria-label={t('actions.addSource')} onClick={onAddFeed}>
            <Icon name="plus" size={15} />
          </button>
        </div>
      </div>
      <nav className="library-tree" aria-label={t('nav.feedTree')}>
        <button
          className={`nav-item library-all-row ${filter === 'all' && feedId === null && folderId === null ? 'active' : ''}`}
          type="button"
          onClick={() => onSelectFilter('all')}
        >
          <Icon name="list" size={17} />
          <span>{t('nav.allFeeds')}</span>
          {unreadTotal > 0 && <span className="feed-unread tree-count">{unreadTotal}</span>}
        </button>
        {visibleFolders.map((folder) => {
          const folderFeeds = feeds.filter((feed) => feed.folderId === folder.id);
          const collapsed = collapsedFolderIds.has(folder.id);
          return <div key={folder.id} className="folder-row library-folder">
            <div className="tree-folder-row">
              <button className="tree-folder-toggle" type="button" aria-label={t(collapsed ? 'folder.expand' : 'folder.collapse', { name: folder.name })} aria-expanded={!collapsed} onClick={() => onToggleFolder(folder.id)}>
                <Icon name="chevron" size={14} />
              </button>
              <button className={`nav-item folder-item ${folderId === folder.id ? 'active' : ''}`} onClick={() => onSelectFolder(folder.id)} title={folder.name}>
                <span className="tree-folder-name">{folder.name}</span>
                {folder.unreadCount > 0 && <span className="feed-unread">{folder.unreadCount}</span>}
              </button>
            </div>
            {!collapsed && <div className="tree-children" role="group" aria-label={folder.name}>{folderFeeds.map(renderFeedRow)}</div>}
          </div>;
        })}
        {feeds.some((feed) => feed.folderId === null) && <div className="folder-row library-folder unfiled-folder">
          <button className="tree-unfiled-toggle" type="button" aria-label={t(unfiledCollapsed ? 'folder.expand' : 'folder.collapse', { name: t('folder.none') })} aria-expanded={!unfiledCollapsed} onClick={onToggleUnfiled}>
            <Icon name="chevron" size={14} />
            <span className="tree-folder-name">{t('folder.none')}</span>
          </button>
          {!unfiledCollapsed && <div className="tree-children" role="group" aria-label={t('folder.none')}>
            {feeds.filter((feed) => feed.folderId === null).map(renderFeedRow)}
          </div>}
        </div>}
        {folders.length === 0 && feeds.length === 0 && <p className="sidebar-hint">{t('states.feedsEmpty')}</p>}
      </nav>

      <div className="sidebar-bottom">
        <div className="sync-indicator"><span className="sync-dot" /><span>{t('nav.localLibrary')}</span></div>
        <ProfileMenu user={user} placement="sidebar" adminActive={adminView} onAdmin={onAdmin} onEditProfile={onEditProfile} onSignOut={onSignOut} />
      </div>
    </aside>
    {mobileNavOpen && <button className="mobile-scrim" aria-label={t('nav.closeMenu')} onClick={onCloseMobileNav} />}
  </>;
}
