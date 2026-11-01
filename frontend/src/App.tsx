import { useCallback, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import './i18n';
import { errorMessage, type Feed, type Folder, type SessionUser } from './api';
import { AdminPanel } from './AdminPanel';
import { Icon } from './AppChrome';
import { ArticleListPane } from './ArticleListPane';
import { ErrorToast } from './ErrorToast';
import { FeedModal } from './FeedModal';
import { FolderDeleteDialog, FolderManager } from './FolderManager';
import { FolderModal } from './FolderModal';
import { useArticleFeed } from './hooks/useArticleFeed';
import { useLibrary, type FolderDeletion } from './hooks/useLibrary';
import { useLibraryView } from './hooks/useLibraryView';
import { useRefresh } from './hooks/useRefresh';
import { useSession } from './hooks/useSession';
import { LibrarySidebar } from './LibrarySidebar';
import { ProfileModal } from './ProfileModal';
import { ReaderPane } from './ReaderPane';
import { SignIn } from './SignIn';
import type { Theme } from './theme';
import { Topbar, type TopbarHandle } from './Topbar';
import { useTheme } from './useTheme';

type Modal =
  | { kind: 'feed' }
  | { kind: 'editFeed'; feed: Feed }
  | { kind: 'folder' }
  | { kind: 'editFolder'; folder: Folder }
  | { kind: 'manageFolders' }
  | { kind: 'deleteFolder'; folder: Folder }
  | { kind: 'profile' };

type ReaderProps = {
  user: SessionUser;
  theme: Theme;
  onToggleTheme: () => void;
  onUserUpdated: (user: SessionUser) => void;
  onSignOut: () => Promise<void>;
  onSignedOut: () => void;
};

/** The signed-in reader: library sidebar, article list, reader and dialogs. */
function Reader({ user, theme, onToggleTheme, onUserUpdated, onSignOut, onSignedOut }: ReaderProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage || 'en';
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [modal, setModal] = useState<Modal | null>(null);
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
          onSignOut={() => void signOut()}
        />

        <section className="content-area">
          {view.adminView ? <AdminPanel currentUserId={user.id} onSignedOut={onSignedOut} /> : <>
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

      {(modal?.kind === 'feed' || modal?.kind === 'editFeed') && <FeedModal
        key={modal.kind === 'editFeed' ? `feed-${modal.feed.id}` : 'feed'}
        feed={modal.kind === 'editFeed' ? modal.feed : null}
        folders={library.folders}
        onClose={closeModal}
        onSubscribed={(created) => void feedSubscribed(created)}
        onUpdated={(updated, urlChanged) => void feedUpdated(updated, urlChanged)}
      />}
      {(modal?.kind === 'folder' || modal?.kind === 'editFolder') && <FolderModal
        key={modal.kind === 'editFolder' ? `folder-${modal.folder.id}` : 'folder'}
        folder={modal.kind === 'editFolder' ? modal.folder : null}
        onClose={closeModal}
        onCreated={(created) => { library.addFolder(created); setModal(null); }}
        onUpdated={(updated) => { library.replaceFolder(updated); setModal(null); }}
      />}
      {modal?.kind === 'manageFolders' && <FolderManager
        folders={library.folders}
        feeds={library.feeds}
        onRename={(folder) => setModal({ kind: 'editFolder', folder })}
        onDelete={(folder) => setModal({ kind: 'deleteFolder', folder })}
        onClose={closeModal}
      />}
      {modal?.kind === 'deleteFolder' && <FolderDeleteDialog folder={modal.folder} folders={library.folders} feeds={library.feeds} onClose={closeModal} onDeleted={folderDeleted} />}
      {modal?.kind === 'profile' && <ProfileModal user={user} onClose={closeModal} onUserUpdated={onUserUpdated} />}
    </div>
  );
}

function App() {
  const { t } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const session = useSession();

  if (session.loading) {
    return <main className="signin-screen" aria-busy="true"><div className="list-loading"><span className="loader" />{t('states.checkingSession')}</div></main>;
  }
  if (!session.user) {
    return <SignIn onLogin={session.signIn} busy={session.signingIn} theme={theme} onToggleTheme={toggleTheme} notice={session.expired ? t('signin.sessionExpired') : ''} />;
  }
  // Keyed by user so a different account never sees the previous library.
  return <Reader key={session.user.id} user={session.user} theme={theme} onToggleTheme={toggleTheme} onUserUpdated={session.setUser} onSignOut={session.signOut} onSignedOut={session.clear} />;
}

export default App;
