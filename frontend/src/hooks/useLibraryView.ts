import { useCallback, useState } from 'react';
import type { Filter } from '../LibrarySidebar';

/**
 * What the reader shows: a filter, one feed, or one folder (feed and folder
 * views use the "all" filter), or the administration workspace; plus the
 * sidebar's navigation state.
 */
export function useLibraryView() {
  const [filter, setFilter] = useState<Filter>('all');
  const [feedId, setFeedId] = useState<number | null>(null);
  const [folderId, setFolderId] = useState<number | null>(null);
  const [adminView, setAdminView] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [collapsedFolderIds, setCollapsedFolderIds] = useState<Set<number>>(() => new Set());
  const [unfiledCollapsed, setUnfiledCollapsed] = useState(false);

  const show = useCallback((next: { filter?: Filter; feedId?: number | null; folderId?: number | null }) => {
    setAdminView(false);
    setFilter(next.filter ?? 'all');
    setFeedId(next.feedId ?? null);
    setFolderId(next.folderId ?? null);
    setMobileNavOpen(false);
  }, []);

  const selectFilter = useCallback((nextFilter: Filter) => show({ filter: nextFilter }), [show]);
  const selectFeed = useCallback((id: number) => show({ feedId: id }), [show]);
  const selectFolder = useCallback((id: number) => show({ folderId: id }), [show]);
  const toggleUnread = useCallback(() => setFilter((current) => current === 'unread' ? 'all' : 'unread'), []);

  /** Returns to all articles without leaving the administration workspace. */
  const resetToAll = useCallback(() => {
    setFilter('all');
    setFeedId(null);
    setFolderId(null);
  }, []);

  const openAdmin = useCallback(() => {
    setAdminView(true);
    setMobileNavOpen(false);
  }, []);

  const toggleFolderCollapsed = useCallback((id: number) => {
    setCollapsedFolderIds((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleUnfiled = useCallback(() => setUnfiledCollapsed((collapsed) => !collapsed), []);

  const forgetFolder = useCallback((id: number) => {
    setCollapsedFolderIds((current) => {
      const next = new Set(current);
      next.delete(id);
      return next;
    });
  }, []);

  return {
    filter,
    feedId,
    folderId,
    adminView,
    mobileNavOpen,
    setMobileNavOpen,
    collapsedFolderIds,
    unfiledCollapsed,
    toggleUnfiled,
    selectFilter,
    selectFeed,
    selectFolder,
    toggleUnread,
    resetToAll,
    openAdmin,
    toggleFolderCollapsed,
    forgetFolder,
  };
}
