import { useTranslation } from 'react-i18next';
import { Icon, ProfileMenu, dateLabel, initials } from './AppChrome';
import './i18n';
import { LanguageSelector } from './LanguageSelector';
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { api, extractArticleImages, folderColors, stripMarkup, type Article, type Feed, type Folder, type FolderColor, type SessionUser } from './api';
import { ThemeToggle } from './ThemeToggle';
import { ArticleDrawer } from './ArticleDrawer';
import { useTheme } from './useTheme';
import { SignIn } from './SignIn';
import { AdminPanel } from './AdminPanel';
import { ErrorToast } from './ErrorToast';
import { FolderPicker } from './FolderPicker';
import { LibrarySidebar, type Filter } from './LibrarySidebar';
import { ArticleCard } from './ArticleCard';

type Modal = 'feed' | 'editFeed' | 'folder' | 'editFolder' | 'manageFolders' | 'deleteFolder' | 'profile' | null;
const articlePageSize = 20;

function App() {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage || 'en';
  const { theme, toggleTheme } = useTheme();
  const [sessionUser, setSessionUser] = useState<SessionUser | null>(null);
  const [sessionLoading, setSessionLoading] = useState(true);
  const [signingIn, setSigningIn] = useState(false);
  const [adminView, setAdminView] = useState(false);
  const [feeds, setFeeds] = useState<Feed[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [articles, setArticles] = useState<Article[]>([]);
  const [filter, setFilter] = useState<Filter>('all');
  const [feedId, setFeedId] = useState<number | null>(null);
  const [folderId, setFolderId] = useState<number | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [search, setSearch] = useState('');
  const [mobileSearchOpen, setMobileSearchOpen] = useState(false);
  const mobileSearchInputRef = useRef<HTMLInputElement>(null);
  const mobileSearchButtonRef = useRef<HTMLButtonElement>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadMoreFailed, setLoadMoreFailed] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [collapsedFolderIds, setCollapsedFolderIds] = useState<Set<number>>(() => new Set());
  const [unfiledCollapsed, setUnfiledCollapsed] = useState(false);
  const [error, setError] = useState('');
  const [modal, setModal] = useState<Modal>(null);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');
  const [formNotice, setFormNotice] = useState('');
  const [profileError, setProfileError] = useState('');
  const [profileNotice, setProfileNotice] = useState('');
  const [feedUrl, setFeedUrl] = useState('');
  const [feedTitle, setFeedTitle] = useState('');
  const [editingFeed, setEditingFeed] = useState<Feed | null>(null);
  const [targetFolder, setTargetFolder] = useState('');
  const [folderName, setFolderName] = useState('');
  const [folderColor, setFolderColor] = useState<FolderColor>('amber');
  const [editingFolder, setEditingFolder] = useState<Folder | null>(null);
  const [managedFolder, setManagedFolder] = useState<Folder | null>(null);
  const [folderDeleteAction, setFolderDeleteAction] = useState<'delete_sources' | 'move_sources'>('delete_sources');
  const [folderDeleteTarget, setFolderDeleteTarget] = useState('');
  const [folderDeleteBusy, setFolderDeleteBusy] = useState(false);

  useEffect(() => {
    if (mobileSearchOpen) mobileSearchInputRef.current?.focus();
  }, [mobileSearchOpen]);
  const [profileCurrentPassword, setProfileCurrentPassword] = useState('');
  const [profileNewPassword, setProfileNewPassword] = useState('');
  const [profileConfirmPassword, setProfileConfirmPassword] = useState('');
  const [profileFirstName, setProfileFirstName] = useState('');
  const [profileLastName, setProfileLastName] = useState('');
  const [profileNickname, setProfileNickname] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const loadMoreSentinelRef = useRef<HTMLDivElement>(null);
  const loadMoreInFlightRef = useRef(false);
  const articleRequestVersionRef = useRef(0);

  useEffect(() => {
    let active = true;
    api.currentUser()
      .then((user) => { if (active) setSessionUser(user); })
      .catch(() => { if (active) setSessionUser(null); })
      .finally(() => { if (active) setSessionLoading(false); });
    return () => { active = false; };
  }, []);

  const loadStructure = useCallback(async () => {
    const [nextFeeds, nextFolders] = await Promise.all([api.getFeeds(), api.getFolders()]);
    setFeeds(nextFeeds);
    setFolders(nextFolders);
  }, []);

  useEffect(() => {
    if (!sessionUser || sessionLoading) return;
    loadStructure()
      .then(() => setError(''))
      .catch((reason: unknown) => setError(reason instanceof Error ? reason.message : t('errors.serverConnection')))
      .finally(() => setLoading(false));
  }, [loadStructure, sessionLoading, sessionUser]);

  useEffect(() => {
    ++articleRequestVersionRef.current;
    if (!sessionUser || sessionLoading) {
      setArticles([]);
      setHasMore(false);
      setLoading(false);
      return;
    }
    let active = true;
    const timer = window.setTimeout(async () => {
      setLoading(true);
      try {
        const params = new URLSearchParams({ filter, limit: String(articlePageSize) });
        if (feedId !== null) params.set('feedId', String(feedId));
        if (folderId !== null) params.set('folderId', String(folderId));
        if (search.trim()) params.set('q', search.trim());
        if (filter === 'today') {
          const now = new Date();
          params.set('since', new Date(now.getFullYear(), now.getMonth(), now.getDate()).toISOString());
        }
        const page = await api.getArticles(params);
        if (!active) return;
        setArticles(page.items);
        setHasMore(page.hasMore);
        setLoadMoreFailed(false);
        setSelectedId((current) => page.items.some((item) => item.id === current) ? current : null);
        setError('');
      } catch (reason) {
        if (active) setError(reason instanceof Error ? reason.message : t('errors.articlesLoad'));
      } finally {
        if (active) setLoading(false);
      }
    }, search ? 220 : 0);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [filter, feedId, folderId, search, refreshVersion, sessionLoading, sessionUser]);

  const selectedArticle = useMemo(() => articles.find((item) => item.id === selectedId) || null, [articles, selectedId]);
  const selectedArticleImages = useMemo(
    () => selectedArticle ? extractArticleImages(selectedArticle.summary, selectedArticle.url) : [],
    [selectedArticle],
  );
  const unreadTotal = feeds.reduce((sum, feed) => sum + feed.unreadCount, 0);
  const activeFeed = feedId === null ? null : feeds.find((item) => item.id === feedId) || null;
  const activeFolder = folderId === null ? null : folders.find((item) => item.id === folderId) || null;
  const isCustomFolderColor = !folderColors.includes(folderColor as typeof folderColors[number]);
  const pageTitle = activeFeed?.title || activeFolder?.name || t(`filters.${filter}`);

  async function refreshStructure() {
    try {
      await loadStructure();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.sourcesRefresh'));
    }
  }

  function selectFilter(nextFilter: Filter) {
    setAdminView(false);
    setFilter(nextFilter);
    setFeedId(null);
    setFolderId(null);
    setMobileNavOpen(false);
  }

  function selectFeed(id: number) {
    setAdminView(false);
    setFeedId(id);
    setFolderId(null);
    setFilter('all');
    setMobileNavOpen(false);
  }

  function selectFolder(id: number) {
    setAdminView(false);
    setFolderId(id);
    setFeedId(null);
    setFilter('all');
    setMobileNavOpen(false);
  }

  function toggleFolderCollapsed(id: number) {
    setCollapsedFolderIds((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function chooseArticle(article: Article) {
    setSelectedId(article.id);
    if (article.isRead) return;
    setArticles((current) => current.map((item) => item.id === article.id ? { ...item, isRead: true } : item));
    try {
      await api.updateArticle(article.id, { isRead: true });
      await refreshStructure();
    } catch (reason) {
      setArticles((current) => current.map((item) => item.id === article.id ? { ...item, isRead: false } : item));
      setError(reason instanceof Error ? reason.message : t('errors.articleStatus'));
    }
  }

  async function toggleRead(article: Article) {
    const isRead = !article.isRead;
    setArticles((current) => current.map((item) => item.id === article.id ? { ...item, isRead } : item));
    try {
      await api.updateArticle(article.id, { isRead });
      await refreshStructure();
      setRefreshVersion((current) => current + 1);
    } catch (reason) {
      setArticles((current) => current.map((item) => item.id === article.id ? { ...item, isRead: article.isRead } : item));
      setError(reason instanceof Error ? reason.message : t('errors.articleStatus'));
    }
  }

  async function toggleSaved(article: Article) {
    const isSaved = !article.isSaved;
    setArticles((current) => current.map((item) => item.id === article.id ? { ...item, isSaved } : item));
    try {
      await api.updateArticle(article.id, { isSaved });
      setRefreshVersion((current) => current + 1);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.articleSave'));
      setArticles((current) => current.map((item) => item.id === article.id ? { ...item, isSaved: article.isSaved } : item));
    }
  }

  async function submitFeed(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError('');
    setFormNotice('');
    setSubmitting(true);
    try {
      const created = await api.createFeed(feedUrl.trim(), targetFolder ? Number(targetFolder) : null);
      if ('requestId' in created) {
        setFormNotice(t('feedModal.requestedApproval'));
        setFeedUrl('');
        return;
      }
      setFeeds((current) => [created, ...current.filter((item) => item.id !== created.id)]);
      setModal(null);
      setFeedUrl('');
      setFilter('all');
      setFolderId(null);
      setFeedId(created.id);
      await refreshStructure();
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : t('errors.feedAdd'));
    } finally {
      setSubmitting(false);
    }
  }

  async function submitFeedEdit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!editingFeed) return;
    setFormError('');
    setFormNotice('');
    setSubmitting(true);
    try {
      const updated = await api.updateFeed(editingFeed.id, {
        title: feedTitle.trim(),
        url: feedUrl.trim(),
        folderId: targetFolder ? Number(targetFolder) : null,
      });
      if ('requestId' in updated) {
        setFormNotice(t('feedModal.requestedApproval'));
        return;
      }
      setFeeds((current) => current.map((feed) => feed.id === updated.id ? updated : feed));
      setModal(null);
      setEditingFeed(null);
      setSelectedId(null);
      setRefreshVersion((current) => current + 1);
      await refreshStructure();
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : t('errors.feedUpdate'));
    } finally {
      setSubmitting(false);
    }
  }

  async function submitFolder(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError('');
    setSubmitting(true);
    try {
      if (modal === 'editFolder' && editingFolder) {
        const updated = await api.updateFolder(editingFolder.id, { name: folderName.trim(), color: folderColor });
        setFolders((current) => current.map((folder) => folder.id === updated.id ? { ...folder, ...updated } : folder));
        setFeeds((current) => current.map((feed) => feed.folderId === updated.id ? { ...feed, folderName: updated.name } : feed));
      } else {
        const created = await api.createFolder(folderName.trim(), folderColor);
        setFolders((current) => [...current, created]);
      }
      setFolderName('');
      setFolderColor('amber');
      setEditingFolder(null);
      setModal(null);
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : t('errors.folderCreate'));
    } finally {
      setSubmitting(false);
    }
  }

  function openEditFolder(folder: Folder) {
    setEditingFolder(folder);
    setFolderName(folder.name);
    setFolderColor(folder.color);
    setFormError('');
    setModal('editFolder');
  }

  function openDeleteFolder(folder: Folder) {
    setManagedFolder(folder);
    setFolderDeleteAction('delete_sources');
    setFolderDeleteTarget(String(folders.find((candidate) => candidate.id !== folder.id)?.id ?? ''));
    setFormError('');
    setModal('deleteFolder');
  }

  async function submitFolderDeletion(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!managedFolder) return;
    setFormError('');
    setFolderDeleteBusy(true);
    try {
      const operation = folderDeleteAction === 'delete_sources'
        ? { action: 'delete_sources' as const }
        : { action: 'move_sources' as const, targetFolderId: Number(folderDeleteTarget) };
      await api.deleteFolder(managedFolder.id, operation);
      const affectedFeedIds = feeds.filter((feed) => feed.folderId === managedFolder.id).map((feed) => feed.id);
      const movedUnreadCount = feeds.filter((feed) => feed.folderId === managedFolder.id).reduce((sum, feed) => sum + feed.unreadCount, 0);
      setFolders((current) => current
        .filter((folder) => folder.id !== managedFolder.id)
        .map((folder) => folder.id === Number(folderDeleteTarget) && folderDeleteAction === 'move_sources'
          ? { ...folder, unreadCount: folder.unreadCount + movedUnreadCount }
          : folder));
      setFeeds((current) => folderDeleteAction === 'delete_sources'
        ? current.filter((feed) => feed.folderId !== managedFolder.id)
        : current.map((feed) => feed.folderId === managedFolder.id
          ? { ...feed, folderId: Number(folderDeleteTarget), folderName: folders.find((folder) => folder.id === Number(folderDeleteTarget))?.name ?? null }
          : feed));
      setCollapsedFolderIds((current) => { const next = new Set(current); next.delete(managedFolder.id); return next; });
      if (folderId === managedFolder.id || (folderDeleteAction === 'delete_sources' && feedId !== null && affectedFeedIds.includes(feedId))) {
        setFolderId(null);
        setFeedId(null);
        setFilter('all');
        setSelectedId(null);
        setRefreshVersion((current) => current + 1);
      }
      setModal(null);
      setManagedFolder(null);
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : t('errors.folderDelete'));
    } finally {
      setFolderDeleteBusy(false);
    }
  }

  async function submitProfileDetails(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setProfileError('');
    setProfileNotice('');
    setSubmitting(true);
    try {
      const updated = await api.updateProfile({
        firstName: profileFirstName.trim(),
        lastName: profileLastName.trim(),
        nickname: profileNickname.trim(),
      });
      setSessionUser(updated);
      setProfileNotice(t('profile.saved'));
    } catch (reason) {
      setProfileError(reason instanceof Error ? reason.message : t('errors.profileSave'));
    } finally {
      setSubmitting(false);
    }
  }

  async function submitProfilePassword(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError('');
    setFormNotice('');
    if (profileNewPassword !== profileConfirmPassword) {
      setFormError(t('profile.passwordMismatch'));
      return;
    }
    setSubmitting(true);
    try {
      await api.changePassword(profileCurrentPassword, profileNewPassword);
      setProfileCurrentPassword('');
      setProfileNewPassword('');
      setProfileConfirmPassword('');
      setFormNotice(t('profile.passwordChanged'));
    } catch (reason) {
      setFormError(reason instanceof Error ? reason.message : t('errors.passwordChange'));
    } finally {
      setSubmitting(false);
    }
  }

  function openProfile() {
    setProfileFirstName(sessionUser?.firstName || '');
    setProfileLastName(sessionUser?.lastName || '');
    setProfileNickname(sessionUser?.nickname || '');
    setProfileCurrentPassword('');
    setProfileNewPassword('');
    setProfileConfirmPassword('');
    setFormError('');
    setFormNotice('');
    setProfileError('');
    setProfileNotice('');
    setModal('profile');
  }

  function openAddFeed() {
    setEditingFeed(null);
    setFeedTitle('');
    setFeedUrl('');
    setTargetFolder('');
    setFormError('');
    setModal('feed');
  }

  function openEditFeed(feed: Feed) {
    setEditingFeed(feed);
    setFeedTitle(feed.title);
    setFeedUrl(feed.url);
    setTargetFolder(feed.folderId ? String(feed.folderId) : '');
    setFormError('');
    setModal('editFeed');
  }

  function openAdmin() {
    setAdminView(true);
    setMobileNavOpen(false);
    setSelectedId(null);
  }

  async function runRefresh() {
    setRefreshing(true);
    try {
      await api.refresh();
      const startedAt = Date.now();
      let stillRunning = true;
      while (stillRunning && Date.now() - startedAt < 180000) {
        await new Promise((resolve) => window.setTimeout(resolve, 750));
        stillRunning = (await api.refreshStatus()).running;
      }
      await refreshStructure();
      setRefreshVersion((current) => current + 1);
      if (stillRunning) setError(t('errors.refreshBackground'));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.refresh'));
    } finally {
      setRefreshing(false);
    }
  }

  const loadMore = useCallback(async () => {
    if (loadMoreInFlightRef.current || !hasMore) return;
    loadMoreInFlightRef.current = true;
    const requestVersion = articleRequestVersionRef.current;
    setLoadingMore(true);
    setLoadMoreFailed(false);
    try {
      const params = new URLSearchParams({ filter, limit: String(articlePageSize), offset: String(articles.length) });
      if (feedId !== null) params.set('feedId', String(feedId));
      if (folderId !== null) params.set('folderId', String(folderId));
      if (search.trim()) params.set('q', search.trim());
      if (filter === 'today') {
        const now = new Date();
        params.set('since', new Date(now.getFullYear(), now.getMonth(), now.getDate()).toISOString());
      }
      const page = await api.getArticles(params);
      if (requestVersion !== articleRequestVersionRef.current) return;
      setArticles((current) => [...current, ...page.items]);
      setHasMore(page.hasMore);
      setError('');
    } catch (reason) {
      if (requestVersion === articleRequestVersionRef.current) {
        setLoadMoreFailed(true);
        setError(reason instanceof Error ? reason.message : t('errors.loadMore'));
      }
    } finally {
      loadMoreInFlightRef.current = false;
      if (requestVersion === articleRequestVersionRef.current) setLoadingMore(false);
    }
  }, [articles.length, feedId, filter, folderId, hasMore, search, t]);

  useEffect(() => {
    const sentinel = loadMoreSentinelRef.current;
    if (!sentinel || !hasMore || loadingMore || loadMoreFailed || loading || adminView) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) void loadMore();
    }, { rootMargin: '300px 0px' });
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [adminView, hasMore, loadMore, loadMoreFailed, loading, loadingMore]);

  const closeModal = () => {
    setModal(null);
    setEditingFeed(null);
    setEditingFolder(null);
    setManagedFolder(null);
    setFormError('');
    setFormNotice('');
    setProfileError('');
    setProfileNotice('');
  };

  async function signIn(email: string, password: string) {
    setSigningIn(true);
    try {
      const user = await api.login(email, password);
      setSessionUser(user);
      setAdminView(false);
      setError('');
      setLoading(true);
    } finally {
      setSigningIn(false);
    }
  }

  async function signOut() {
    try {
      await api.logout();
      setSessionUser(null);
      setAdminView(false);
      setFeeds([]);
      setFolders([]);
      setArticles([]);
      setSelectedId(null);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.signOut'));
    }
  }


  if (sessionLoading) {
    return <main className="signin-screen" aria-busy="true"><div className="list-loading"><span className="loader" />{t('states.checkingSession')}</div></main>;
  }
  if (!sessionUser) {
    return <SignIn onLogin={signIn} busy={signingIn} theme={theme} onToggleTheme={toggleTheme} />;
  }

  return (
    <div className="app-shell" inert={Boolean(selectedArticle)}>
      <LibrarySidebar
        user={sessionUser}
        feeds={feeds}
        folders={folders}
        filter={filter}
        feedId={feedId}
        folderId={folderId}
        unreadTotal={unreadTotal}
        adminView={adminView}
        mobileNavOpen={mobileNavOpen}
        collapsedFolderIds={collapsedFolderIds}
        unfiledCollapsed={unfiledCollapsed}
        onHome={() => selectFilter('all')}
        onAddFeed={openAddFeed}
        onCreateFolder={() => { setEditingFolder(null); setFolderName(''); setFolderColor('amber'); setFormError(''); setModal('folder'); }}
        onManageFolders={() => { setFormError(''); setModal('manageFolders'); }}
        onSelectFilter={selectFilter}
        onSelectFeed={selectFeed}
        onSelectFolder={selectFolder}
        onToggleFolder={toggleFolderCollapsed}
        onToggleUnfiled={() => setUnfiledCollapsed((collapsed) => !collapsed)}
        onEditFeed={openEditFeed}
        onAdmin={openAdmin}
        onEditProfile={openProfile}
        onSignOut={() => void signOut()}
        onCloseMobileNav={() => setMobileNavOpen(false)}
      />

      <main className="main-area">
        <header className="topbar">
          <div className="mobile-brand">
            <button className="icon-button mobile-menu" aria-label={t('nav.openMenu')} aria-expanded={mobileNavOpen} onClick={() => setMobileNavOpen((open) => !open)}><Icon name="menu" size={19} /></button>
            <span className="brand-mark"><Icon name="rss" size={19} /></span><span className="brand-name">Pulse<span>.</span></span>
          </div>
          <div className="topbar-search">
            <Icon name="search" size={18} />
            <input aria-label={t('search.label')} value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t('search.placeholder')} />
          </div>
          <button ref={mobileSearchButtonRef} className="icon-button mobile-search-trigger" aria-label={t('search.label')} aria-expanded={mobileSearchOpen} onClick={() => setMobileSearchOpen(true)}><Icon name="search" size={19} /></button>
          {mobileSearchOpen && <div className="mobile-search-panel">
            <Icon name="search" size={19} />
            <input ref={mobileSearchInputRef} aria-label={t('search.label')} value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t('search.placeholder')} onKeyDown={(event) => { if (event.key === 'Escape') { setMobileSearchOpen(false); mobileSearchButtonRef.current?.focus(); } }} />
            <button className="icon-button" aria-label={t('actions.close')} onClick={() => { setMobileSearchOpen(false); mobileSearchButtonRef.current?.focus(); }}><Icon name="close" size={17} /></button>
          </div>}
          <div className="topbar-actions">
            <ThemeToggle theme={theme} onToggle={toggleTheme} />
            <LanguageSelector />
            <button className={`icon-button refresh-button ${refreshing ? 'spinning' : ''}`} aria-label={t('actions.refresh')} title={t('actions.refresh')} onClick={() => void runRefresh()} disabled={refreshing}>
              <Icon name="refresh" size={18} />
            </button>
            <span className="topbar-divider" />
            <ProfileMenu user={sessionUser} placement="topbar" adminActive={adminView} onAdmin={openAdmin} onEditProfile={openProfile} onSignOut={() => void signOut()} />
          </div>
        </header>

        <section className="content-area">
          {adminView ? <AdminPanel /> : <>
          <div className="page-heading">
            <div>
              <h1>{pageTitle}</h1>
              <p className="page-subtitle">{t('reader.subtitle')}</p>
            </div>
            <button className="primary-button heading-add" aria-label={t('actions.addFeed')} onClick={openAddFeed}><Icon name="plus" size={19} /><span className="heading-add-label">{t('actions.addFeed')}</span></button>
          </div>

          <div className="reader-grid">
            <section className="article-column" aria-label={t('reader.articleList')}>
              <div className="list-toolbar">
                <div className="list-toolbar-label"><span className="live-dot" />{filter === 'all' ? t('reader.yourFeed') : t(`filters.${filter}`)}<span className="toolbar-count">{articles.length}</span></div>
                <button className="sort-button" onClick={() => setFilter((current) => current === 'unread' ? 'all' : 'unread')} title={t('filters.toggleUnread')}>
                  <span className={`unread-toggle ${filter === 'unread' ? 'checked' : ''}`}>{filter === 'unread' && <Icon name="check" size={11} />}</span>
                  {t('filters.unread')}
                </button>
              </div>

              <div className="article-list">
                {loading && articles.length === 0 ? <div className="list-loading"><span className="loader" />{t('states.loadingFeed')}</div> : null}
                {!loading && articles.length === 0 ? (
                  <div className="empty-list">
                    <span className="empty-icon"><Icon name={search ? 'search' : 'inbox'} size={22} /></span>
                    <strong>{search ? t('states.noResults') : filter === 'saved' ? t('states.nothingSaved') : t('states.quiet')}</strong>
                    <span>{search ? t('states.tryAnotherSearch') : t('states.addFeedHint')}</span>
                    {!search && filter !== 'saved' && <button className="text-action" onClick={openAddFeed}>{t('actions.addSource')}<Icon name="arrow" size={15} /></button>}
                  </div>
                ) : articles.map((article) => (
                  <ArticleCard
                    key={article.id}
                    article={article}
                    selected={selectedId === article.id}
                    locale={locale}
                    folderName={feeds.find((item) => item.id === article.feedId)?.folderName ?? null}
                    onChoose={() => void chooseArticle(article)}
                    onToggleRead={() => void toggleRead(article)}
                    onToggleSaved={() => void toggleSaved(article)}
                  />
                ))}
              </div>
              {hasMore && <div className="load-more-wrap" ref={loadMoreSentinelRef}>
                {loadingMore ? <span className="load-more-status" role="status"><span className="button-spinner" />{t('states.loading')}</span> : loadMoreFailed ? <button className="load-more-button" onClick={() => void loadMore()}>{t('actions.loadMore')}</button> : null}
              </div>}
              {articles.length > 0 && !hasMore && <div className="list-bottom-note"><span className="bottom-rule" />{t('states.endOfList')}<span className="bottom-rule" /></div>}
            </section>

            {selectedArticle && <ArticleDrawer onClose={() => setSelectedId(null)}>
              <section className="reading-pane" aria-label={t('article.selected')}>
                <div className="reading-scroll">
                  <div className="reading-toolbar"><span className="reading-source"><span className={`source-mark source-mark-${Math.abs(selectedArticle.feedId) % 5}`}>{initials(selectedArticle.feedTitle)}</span>{selectedArticle.feedTitle}</span><div className="reading-actions"><button className="icon-button" aria-label={selectedArticle.isRead ? t('actions.markUnread') : t('actions.markRead')} title={selectedArticle.isRead ? t('actions.markUnread') : t('actions.markRead')} onClick={() => void toggleRead(selectedArticle)}><Icon name={selectedArticle.isRead ? 'inbox' : 'check'} size={17} /></button><button className={`icon-button ${selectedArticle.isSaved ? 'active-save' : ''}`} aria-label={t('actions.saveArticle')} title={t('actions.saveForLater')} onClick={() => void toggleSaved(selectedArticle)}><Icon name="bookmark" size={18} /></button>{selectedArticle.url && <a className="icon-button" href={selectedArticle.url} target="_blank" rel="noreferrer" aria-label={t('actions.openOriginal')} title={t('actions.openOriginal')}><Icon name="external" size={17} /></a>}</div></div>
                  <div className="reading-article">
                    <div className="reading-kicker"><span className="kicker-line" />{selectedArticle.publishedAt ? new Intl.DateTimeFormat(locale, { dateStyle: 'long' }).format(new Date(selectedArticle.publishedAt)) : t('article.new')}</div>
                    <h2>{selectedArticle.title}</h2>
                    <div className="reading-byline"><span className="byline-avatar">{initials(selectedArticle.feedTitle)}</span><span><strong>{selectedArticle.feedTitle}</strong><small>{t('article.feedSource')}</small></span><span className="byline-dot">·</span><span className="reading-time">{dateLabel(selectedArticle.publishedAt, locale)}</span></div>
                    {selectedArticleImages.length > 0 && <div className="reading-images" role="group" aria-label={t('article.images')}>{selectedArticleImages.map((image) => <img key={image.src} src={image.src} alt={image.alt || selectedArticle.title} loading="lazy" referrerPolicy="no-referrer" />)}</div>}
                    <div className="reading-divider" />
                    <p className="reading-lead">{stripMarkup(selectedArticle.summary) || t('article.fullVersionFallback')}</p>
                    <div className="source-note"><Icon name="sparkle" size={17} /><span>{t('article.sourceNote')}</span></div>
                    {selectedArticle.url && <a className="read-original" href={selectedArticle.url} target="_blank" rel="noreferrer">{t('actions.readOriginal')} <Icon name="arrow" size={16} /></a>}
                  </div>
                  <div className="reader-footer"><span>{t('article.end')}</span><span className="footer-line" /></div>
                </div>
              </section>
            </ArticleDrawer>}
          </div>
          </>}
        </section>
        <ErrorToast message={error} onDismiss={() => setError('')} />
      </main>

      {modal && <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) closeModal(); }}>
        <section className="modal-card" role="dialog" aria-modal="true" aria-labelledby="modal-title">
          <button className="icon-button modal-close" onClick={closeModal} aria-label={t('actions.close')}><Icon name="close" size={19} /></button>
          <div className="modal-icon"><Icon name={modal === 'feed' ? 'rss' : modal === 'editFeed' || modal === 'editFolder' ? 'edit' : modal === 'folder' ? 'folder' : modal === 'manageFolders' ? 'settings' : modal === 'deleteFolder' ? 'trash' : 'user'} size={21} /></div>
          <p className="modal-eyebrow">{modal === 'profile' ? t('profile.settings') : modal === 'manageFolders' || modal === 'deleteFolder' ? t('folder.manage') : t('reader.library')}</p>
          <h2 id="modal-title">{modal === 'feed' ? t('actions.addSource') : modal === 'editFeed' ? t('feedModal.editTitle') : modal === 'editFolder' ? t('folder.edit') : modal === 'folder' ? t('folder.new') : modal === 'manageFolders' ? t('folder.manage') : modal === 'deleteFolder' ? t('folder.deleteTitle') : t('profile.title')}</h2>
          <p className="modal-copy">{modal === 'feed' ? t('feedModal.description') : modal === 'editFeed' ? t('feedModal.editDescription') : modal === 'folder' || modal === 'editFolder' ? t('folderModal.description') : modal === 'manageFolders' ? t('folder.manageDescription') : modal === 'deleteFolder' ? (feeds.some((feed) => feed.folderId === managedFolder?.id) ? t('folder.deleteWarning', { name: managedFolder?.name ?? '', count: feeds.filter((feed) => feed.folderId === managedFolder?.id).length }) : t('folder.deleteEmptyWarning', { name: managedFolder?.name ?? '' })) : t('profile.description')}</p>
          {modal === 'manageFolders' ? <div className="folder-management-list">
            {folders.length === 0 ? <p className="folder-management-empty">{t('folder.manageEmpty')}</p> : folders.map((folder) => <div className="folder-management-row" key={folder.id}>
              <div className="folder-management-copy"><strong>{folder.name}</strong><span>{t('folder.sourceCount', { count: feeds.filter((feed) => feed.folderId === folder.id).length })}</span></div>
              <button className="folder-management-action" type="button" aria-label={t('folder.renameFor', { name: folder.name })} title={t('folder.rename')} onClick={() => openEditFolder(folder)}><Icon name="edit" size={16} /></button>
              <button className="folder-management-action folder-management-delete" type="button" aria-label={t('folder.deleteFor', { name: folder.name })} title={t('folder.delete')} onClick={() => openDeleteFolder(folder)}><Icon name="trash" size={16} /></button>
            </div>)}
            <ErrorToast message={formError} onDismiss={() => setFormError('')} />
          </div> : modal === 'deleteFolder' ? <form className="folder-delete-form" onSubmit={(event) => void submitFolderDeletion(event)}>
            {feeds.some((feed) => feed.folderId === managedFolder?.id) && <fieldset className="folder-delete-options">
              <legend className="form-label">{t('folder.deleteChoice')}</legend>
              <label className="folder-delete-option"><input type="radio" name="folder-delete-action" checked={folderDeleteAction === 'delete_sources'} onChange={() => setFolderDeleteAction('delete_sources')} /><span><strong>{t('folder.deleteSources')}</strong><small>{t('folder.deleteSourcesDescription')}</small></span></label>
              {folders.some((folder) => folder.id !== managedFolder?.id) && <label className="folder-delete-option"><input type="radio" name="folder-delete-action" checked={folderDeleteAction === 'move_sources'} onChange={() => setFolderDeleteAction('move_sources')} /><span><strong>{t('folder.moveSources')}</strong><small>{t('folder.moveSourcesDescription')}</small></span></label>}
              {folderDeleteAction === 'move_sources' && <div className="folder-delete-target"><label className="form-label" htmlFor="folder-delete-target">{t('folder.moveTo')}</label><select id="folder-delete-target" required value={folderDeleteTarget} onChange={(event) => setFolderDeleteTarget(event.target.value)}>{folders.filter((folder) => folder.id !== managedFolder?.id).map((folder) => <option key={folder.id} value={folder.id}>{folder.name}</option>)}</select></div>}
            </fieldset>}
            <ErrorToast message={formError} onDismiss={() => setFormError('')} />
            <button className="folder-delete-confirm" type="submit" disabled={folderDeleteBusy}>{folderDeleteBusy ? t('folder.deleting') : t('folder.confirmDelete')}</button>
          </form> : modal === 'profile' ? <>
            <div className="profile-summary-card">
              <span className="profile-dropdown-avatar">{initials(sessionUser.email)}</span>
              <span><strong>{sessionUser.email}</strong><small>{sessionUser.role === 'Administrator' ? t('roles.administrator') : t('roles.reader')}</small></span>
            </div>
            <form onSubmit={(event) => void submitProfileDetails(event)}>
              <p className="profile-form-title">{t('profile.personalDetails')}</p>
              <label className="form-label" htmlFor="profile-first-name">{t('profile.firstName')} <span>{t('common.optional')}</span></label>
              <input className="text-input" id="profile-first-name" autoComplete="given-name" autoFocus maxLength={80} value={profileFirstName} onChange={(event) => setProfileFirstName(event.target.value)} />
              <label className="form-label folder-label" htmlFor="profile-last-name">{t('profile.lastName')} <span>{t('common.optional')}</span></label>
              <input className="text-input" id="profile-last-name" autoComplete="family-name" maxLength={80} value={profileLastName} onChange={(event) => setProfileLastName(event.target.value)} />
              <label className="form-label folder-label" htmlFor="profile-nickname">{t('profile.nickname')} <span>{t('common.optional')}</span></label>
              <input className="text-input" id="profile-nickname" autoComplete="nickname" maxLength={40} value={profileNickname} onChange={(event) => setProfileNickname(event.target.value)} />
              <ErrorToast message={profileError} onDismiss={() => setProfileError('')} />
              {profileNotice && <p className="profile-success" role="status">{profileNotice}</p>}
              <button className="primary-button modal-submit" type="submit" disabled={submitting}>{submitting ? t('states.saving') : t('profile.save')}</button>
            </form>
            <form className="profile-password-form" onSubmit={(event) => void submitProfilePassword(event)}>
              <p className="profile-form-title">{t('profile.changePassword')}</p>
            <label className="form-label" htmlFor="profile-current-password">{t('profile.currentPassword')}</label>
            <input className="text-input" id="profile-current-password" type="password" autoComplete="current-password" required value={profileCurrentPassword} onChange={(event) => setProfileCurrentPassword(event.target.value)} />
            <label className="form-label folder-label" htmlFor="profile-new-password">{t('profile.newPassword')}</label>
            <input className="text-input" id="profile-new-password" type="password" autoComplete="new-password" minLength={12} required value={profileNewPassword} onChange={(event) => setProfileNewPassword(event.target.value)} placeholder={t('profile.passwordHint')} />
            <label className="form-label folder-label" htmlFor="profile-confirm-password">{t('profile.confirmPassword')}</label>
            <input className="text-input" id="profile-confirm-password" type="password" autoComplete="new-password" minLength={12} required value={profileConfirmPassword} onChange={(event) => setProfileConfirmPassword(event.target.value)} />
            <ErrorToast message={formError} onDismiss={() => setFormError('')} />
            {formNotice && <p className="profile-success" role="status">{formNotice}</p>}
            <button className="primary-button modal-submit" type="submit" disabled={submitting}>{submitting ? t('states.saving') : t('profile.savePassword')}</button>
            </form>
          </> : (modal === 'feed' || modal === 'editFeed') ? <form className="feed-form" onSubmit={modal === 'editFeed' ? submitFeedEdit : submitFeed}>
            {modal === 'editFeed' && <>
              <div className="form-field">
                <label className="form-label" htmlFor="feed-title">{t('feedModal.title')}</label>
                <input className="text-input" id="feed-title" autoFocus maxLength={120} required value={feedTitle} onChange={(event) => setFeedTitle(event.target.value)} />
              </div>
            </>}
            <div className="form-field">
              <label className="form-label" htmlFor="feed-url">{t('feedModal.url')}</label>
              <div className="url-input-wrap"><Icon name="rss" size={17} /><input id="feed-url" type="url" required autoFocus={modal === 'feed'} value={feedUrl} onChange={(event) => setFeedUrl(event.target.value)} placeholder="https://example.com/feed.xml" /></div>
            </div>
            <div className="form-field">
              <label className="form-label" id="feed-folder-label" htmlFor="feed-folder">{t('folder.label')} <span>{t('common.optional')}</span></label>
              <FolderPicker id="feed-folder" labelId="feed-folder-label" folders={folders} value={targetFolder} noneLabel={t('folder.none')} onChange={setTargetFolder} />
            </div>
            <ErrorToast message={formError} onDismiss={() => setFormError('')} />
            {formNotice && <p className="profile-success" role="status">{formNotice}</p>}
            <button className="primary-button modal-submit" type="submit" disabled={submitting}>{submitting ? <><span className="button-spinner" />{modal === 'editFeed' ? t('states.saving') : t('states.checkingFeed')}</> : modal === 'editFeed' ? <>{t('actions.saveChanges')}<Icon name="arrow" size={16} /></> : <>{t('actions.subscribe')}<Icon name="arrow" size={16} /></>}</button>
          </form> : <form onSubmit={(event) => void submitFolder(event)}>
            <label className="form-label" htmlFor="folder-name">{t('folder.name')}</label>
            <input className="text-input" id="folder-name" autoFocus maxLength={60} required value={folderName} onChange={(event) => setFolderName(event.target.value)} placeholder={t('folder.placeholder')} />
            <fieldset className="folder-color-fieldset">
              <legend className="form-label">{t('folder.color')}</legend>
              <div className="folder-color-options" role="radiogroup" aria-label={t('folder.color')}>
                {folderColors.map((color) => <button key={color} type="button" role="radio" aria-checked={folderColor === color} aria-label={t('folder.colorOption', { color: t(`folder.color.${color}`) })} className={`folder-color-option folder-color-${color} ${folderColor === color ? 'selected' : ''}`} onClick={() => setFolderColor(color)} />)}
              </div>
              <label className={`folder-custom-color ${isCustomFolderColor ? 'selected' : ''}`}>
                <span>{t('folder.customColor')}</span>
                <input type="color" aria-label={t('folder.customColor')} value={isCustomFolderColor ? folderColor : '#ffffff'} onChange={(event) => setFolderColor(event.target.value.toLowerCase())} />
              </label>
            </fieldset>
            <ErrorToast message={formError} onDismiss={() => setFormError('')} />
            <button className="primary-button modal-submit" type="submit" disabled={submitting}>{submitting ? t('states.saving') : <>{modal === 'editFolder' ? t('actions.saveChanges') : t('actions.createFolder')} <Icon name="arrow" size={16} /></>}</button>
          </form>}
          {modal !== 'manageFolders' && modal !== 'deleteFolder' && <div className="modal-footnote"><span className="secure-dot" />{modal === 'profile' ? t('profile.passwordHelp') : modal === 'editFeed' ? t('feedModal.urlChangeNote') : t('profile.localStorageNote')}</div>}
        </section>
      </div>}
    </div>
  );
}

export default App;
