import { useCallback, useEffect, useEffectEvent, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, errorMessage, isAbortError, type Feed, type Folder } from '../api';

export type FolderDeletion = { action: 'delete_sources' } | { action: 'move_sources'; targetFolderId: number };

/** The signed-in user's subscriptions and folders with their unread counters. */
export function useLibrary(onError: (message: string) => void) {
  const { t } = useTranslation();
  const [feeds, setFeeds] = useState<Feed[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);

  const reportLoadError = useEffectEvent((reason: unknown) => onError(errorMessage(reason, t('errors.serverConnection'))));

  useEffect(() => {
    const controller = new AbortController();
    Promise.all([api.getFeeds(controller.signal), api.getFolders(controller.signal)])
      .then(([nextFeeds, nextFolders]) => {
        if (controller.signal.aborted) return;
        setFeeds(nextFeeds);
        setFolders(nextFolders);
      })
      .catch((reason: unknown) => { if (!controller.signal.aborted && !isAbortError(reason)) reportLoadError(reason); });
    return () => controller.abort();
  }, []);

  /** Reloads feeds and folders, for example after unread counters changed. */
  const refreshStructure = useCallback(async () => {
    try {
      const [nextFeeds, nextFolders] = await Promise.all([api.getFeeds(), api.getFolders()]);
      setFeeds(nextFeeds);
      setFolders(nextFolders);
    } catch (reason) {
      onError(errorMessage(reason, t('errors.sourcesRefresh')));
    }
  }, [onError, t]);

  const addFeed = useCallback((created: Feed) => {
    setFeeds((current) => [created, ...current.filter((item) => item.id !== created.id)]);
  }, []);

  const replaceFeed = useCallback((updated: Feed) => {
    setFeeds((current) => current.map((feed) => feed.id === updated.id ? updated : feed));
  }, []);

  const addFolder = useCallback((created: Folder) => setFolders((current) => [...current, created]), []);

  const replaceFolder = useCallback((updated: Folder) => {
    setFolders((current) => current.map((folder) => folder.id === updated.id ? { ...folder, ...updated } : folder));
    setFeeds((current) => current.map((feed) => feed.folderId === updated.id ? { ...feed, folderName: updated.name } : feed));
  }, []);

  /** Applies a completed folder deletion to the local feeds and counters. */
  const removeFolder = useCallback((removed: Folder, deletion: FolderDeletion) => {
    const targetId = deletion.action === 'move_sources' ? deletion.targetFolderId : null;
    const targetName = folders.find((folder) => folder.id === targetId)?.name ?? null;
    const movedUnreadCount = feeds.filter((feed) => feed.folderId === removed.id).reduce((sum, feed) => sum + feed.unreadCount, 0);
    setFolders((current) => current
      .filter((folder) => folder.id !== removed.id)
      .map((folder) => folder.id === targetId ? { ...folder, unreadCount: folder.unreadCount + movedUnreadCount } : folder));
    setFeeds((current) => targetId === null
      ? current.filter((feed) => feed.folderId !== removed.id)
      : current.map((feed) => feed.folderId === removed.id ? { ...feed, folderId: targetId, folderName: targetName } : feed));
  }, [feeds, folders]);

  return { feeds, folders, refreshStructure, addFeed, replaceFeed, addFolder, replaceFolder, removeFolder };
}
