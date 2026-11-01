import { lazy, Suspense, useCallback, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { errorMessage, type Feed, type Folder, type SessionUser } from './api';
import { Icon } from './AppChrome';
import { ArticleListPane } from './ArticleListPane';
import { ErrorToast } from './ErrorToast';
import { useArticleFeed } from './hooks/useArticleFeed';
import { useKeyboardShortcuts, type ShortcutAction } from './hooks/useKeyboardShortcuts';
import { useLibrary, type FolderDeletion } from './hooks/useLibrary';
import { useLibraryView } from './hooks/useLibraryView';
import { useRefresh } from './hooks/useRefresh';
import { LibrarySidebar } from './LibrarySidebar';
import { ReaderDialogs, type ReaderDialog } from './ReaderDialogs';
import { ReaderPane } from './ReaderPane';
import type { Theme } from './theme';
import { Topbar, type TopbarHandle } from './Topbar';

// Only administrators open the admin workspace; load it on demand.
const AdminPanel = lazy(() => import('./AdminPanel').then((module) => ({ default: module.AdminPanel })));

type ReaderProps = {
  user: SessionUser;
  theme: Theme;
  onToggleTheme: () => void;
  onUserUpdated: (user: SessionUser) => void;
  onSignOut: () => Promise<void>;
  onSignedOut: () => void;
};

/** The signed-in reader: library sidebar, article list, reader and dialogs. */
export function Reader({ user, theme, onToggleTheme, onUserUpdated, onSignOut, onSignedOut }: ReaderProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage || 'en';
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [modal, setModal] = useState<ReaderDialog | null>(null);
  const topbarRef = useRef<TopbarHandle>(null);
  const view = useLibraryView();
  const library = useLibrary(setError);
  const { refreshStructure } = library;
  const feed = useArticleFeed(
    { filter: view.filter, feedId: view.feedId, folderId: view.folderId, search },
    { onError: setError, onCountsChanged: refreshStructure },
  );
  const { reload, selectedArticle } = feed;
  const refreshFinished = useCallback(async () => {
    await refreshStructure();
    reload();
  }, [refreshStructure, reload]);
  const { refreshing, runRefresh } = useRefresh({ onError: setError, onFinished: refreshFinished });

  const unreadTotal = library.feeds.reduce((sum, item) => sum + item.unreadCount, 0);
  const activeFeed = view.feedId === null ? null : library.feeds.find((item) => item.id === view.feedId) || null;
  const activeFolder = view.folderId === null ? null : library.folders.find((item) => item.id === view.folderId) || null;
  const pageTitle = activeFeed?.title || activeFolder?.name || t(`filters.${view.filter}`);
  const closeModal = () => setModal(null);

  function openAdmin() {
    view.openAdmin();
    feed.setSelectedId(null);
  }

  async function signOut() {
    try {
      await onSignOut();
    } catch (reason) {
      setError(errorMessage(reason, t('errors.signOut')));
    }
  }

  async function feedSubscribed(created: Feed) {
    library.addFeed(created);
    setModal(null);
    view.selectFeed(created.id);
    await refreshStructure();
  }

  async function feedUpdated(updated: Feed, urlChanged: boolean) {
    library.replaceFeed(updated);
    setModal(null);
    feed.setSelectedId(null);
    reload();
    await refreshStructure();
    // The server fetches a changed URL in the background; follow that refresh.
    if (urlChanged) void runRefresh(false);
  }

  function folderDeleted(folder: Folder, deletion: FolderDeletion) {
    const affectedFeedIds = library.feeds.filter((item) => item.folderId === folder.id).map((item) => item.id);
    library.removeFolder(folder, deletion);
    view.forgetFolder(folder.id);
    if (view.folderId === folder.id || (deletion.action === 'delete_sources' && view.feedId !== null && affectedFeedIds.includes(view.feedId))) {
      view.resetToAll();
      feed.setSelectedId(null);
      reload();
    }
    setModal(null);
  }

  function requestMarkAllRead() {
    // Ask first when the view may hold more unread articles than are visible.
    if (feed.hasMore || feed.articles.filter((article) => !article.isRead).length > 10) setModal({ kind: 'markAllRead' });
    else feed.markAllRead().catch((reason: unknown) => setError(errorMessage(reason, t('errors.markAllRead'))));
  }

  function runShortcut(action: ShortcutAction) {
    switch (action) {
      case 'next': return feed.selectRelative(1);
      case 'previous': return feed.selectRelative(-1);
      case 'openOriginal': if (selectedArticle?.url) window.open(selectedArticle.url, '_blank', 'noopener,noreferrer'); return;
      case 'toggleRead': if (selectedArticle) void feed.toggleRead(selectedArticle); return;
      case 'toggleSaved': if (selectedArticle) void feed.toggleSaved(selectedArticle); return;
      case 'refresh': if (!refreshing) void runRefresh(); return;
      case 'focusSearch':
        // The open reader makes the page inert; close it before focusing.
        feed.setSelectedId(null);
        window.requestAnimationFrame(() => topbarRef.current?.focusSearch());
        return;
      case 'showHelp':
        feed.setSelectedId(null);
        setModal({ kind: 'shortcuts' });
    }
  }
  useKeyboardShortcuts(modal === null && !view.adminView, runShortcut);

  return (
    <div className="app-shell" inert={Boolean(selectedArticle)}>
      <LibrarySidebar
        user={user}
        feeds={library.feeds}
        folders={library.folders}
        filter={view.filter}
        feedId={view.feedId}
        folderId={view.folderId}
        unreadTotal={unreadTotal}
        adminView={view.adminView}
        mobileNavOpen={view.mobileNavOpen}
        collapsedFolderIds={view.collapsedFolderIds}
        unfiledCollapsed={view.unfiledCollapsed}
        onHome={() => view.selectFilter('all')}
        onAddFeed={() => setModal({ kind: 'feed' })}
        onCreateFolder={() => setModal({ kind: 'folder' })}
        onManageFolders={() => setModal({ kind: 'manageFolders' })}
        onSelectFilter={view.selectFilter}
        onSelectFeed={view.selectFeed}
        onSelectFolder={view.selectFolder}
        onToggleFolder={view.toggleFolderCollapsed}
        onToggleUnfiled={view.toggleUnfiled}
        onEditFeed={(item) => setModal({ kind: 'editFeed', feed: item })}
        onAdmin={openAdmin}
        onEditProfile={() => setModal({ kind: 'profile' })}
        onImportSubscriptions={() => setModal({ kind: 'importOpml' })}
        onShowShortcuts={() => setModal({ kind: 'shortcuts' })}
        onSignOut={() => void signOut()}
        onCloseMobileNav={() => view.setMobileNavOpen(false)}
      />

      <main className="main-area">
        <Topbar
          ref={topbarRef}
          user={user}
          search={search}
          onSearchChange={setSearch}
          theme={theme}
          onToggleTheme={onToggleTheme}
          refreshing={refreshing}
          onRefresh={() => void runRefresh()}
          mobileNavOpen={view.mobileNavOpen}
          onToggleMobileNav={() => view.setMobileNavOpen((open) => !open)}
          adminActive={view.adminView}
          onAdmin={openAdmin}
          onEditProfile={() => setModal({ kind: 'profile' })}
          onImportSubscriptions={() => setModal({ kind: 'importOpml' })}
          onShowShortcuts={() => setModal({ kind: 'shortcuts' })}
          onSignOut={() => void signOut()}
        />

        <section className="content-area">
          {view.adminView ? <Suspense fallback={<div className="list-loading" aria-busy="true"><span className="loader" />{t('states.loading')}</div>}>
            <AdminPanel currentUserId={user.id} onSignedOut={onSignedOut} />
          </Suspense> : <>
          <div className="page-heading">
            <div>
              <h1>{pageTitle}</h1>
              <p className="page-subtitle">{t('reader.subtitle')}</p>
            </div>
            <button className="primary-button heading-add" aria-label={t('actions.addFeed')} onClick={() => setModal({ kind: 'feed' })}><Icon name="plus" size={19} /><span className="heading-add-label">{t('actions.addFeed')}</span></button>
          </div>

          <div className="reader-grid">
            <ArticleListPane
              filter={view.filter}
              search={search}
              locale={locale}
              feeds={library.feeds}
              articles={feed.articles}
              selectedId={feed.selectedId}
              loading={feed.loading}
              hasMore={feed.hasMore}
              loadingMore={feed.loadingMore}
              loadMoreFailed={feed.loadMoreFailed}
              onToggleUnreadFilter={view.toggleUnread}
              onMarkAllRead={requestMarkAllRead}
              onAddFeed={() => setModal({ kind: 'feed' })}
              onChoose={(article) => void feed.chooseArticle(article)}
              onToggleRead={(article) => void feed.toggleRead(article)}
              onToggleSaved={(article) => void feed.toggleSaved(article)}
              onLoadMore={() => void feed.loadMore()}
            />
            {selectedArticle && <ReaderPane
              article={selectedArticle}
              locale={locale}
              onClose={() => feed.setSelectedId(null)}
              onToggleRead={() => void feed.toggleRead(selectedArticle)}
              onToggleSaved={() => void feed.toggleSaved(selectedArticle)}
            />}
          </div>
          </>}
        </section>
        <ErrorToast message={error} onDismiss={() => setError('')} />
      </main>

      <ReaderDialogs
        dialog={modal}
        onOpen={setModal}
        onClose={closeModal}
        user={user}
        feeds={library.feeds}
        folders={library.folders}
        viewTitle={pageTitle}
        onFeedSubscribed={(created) => void feedSubscribed(created)}
        onFeedUpdated={(updated, urlChanged) => void feedUpdated(updated, urlChanged)}
        onFolderCreated={(created) => { library.addFolder(created); setModal(null); }}
        onFolderUpdated={(updated) => { library.replaceFolder(updated); setModal(null); }}
        onFolderDeleted={folderDeleted}
        onUserUpdated={onUserUpdated}
        onMarkAllRead={feed.markAllRead}
        onImported={() => { void refreshStructure(); reload(); }}
      />
    </div>
  );
}
