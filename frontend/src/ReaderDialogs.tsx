import type { Feed, Folder, SessionUser } from './api';
import { FeedModal } from './FeedModal';
import { FolderDeleteDialog, FolderManager } from './FolderManager';
import { FolderModal } from './FolderModal';
import type { FolderDeletion } from './hooks/useLibrary';
import { MarkAllReadDialog } from './MarkAllReadDialog';
import { OpmlImportModal } from './OpmlImportModal';
import { ProfileModal } from './ProfileModal';
import { ShortcutsDialog } from './ShortcutsDialog';

/** The dialog the reader shows, with the data it edits. */
export type ReaderDialog =
  | { kind: 'feed' }
  | { kind: 'editFeed'; feed: Feed }
  | { kind: 'folder' }
  | { kind: 'editFolder'; folder: Folder }
  | { kind: 'manageFolders' }
  | { kind: 'deleteFolder'; folder: Folder }
  | { kind: 'profile' }
  | { kind: 'markAllRead' }
  | { kind: 'importOpml' }
  | { kind: 'shortcuts' };

type Props = {
  dialog: ReaderDialog | null;
  onOpen: (dialog: ReaderDialog) => void;
  onClose: () => void;
  user: SessionUser;
  feeds: Feed[];
  folders: Folder[];
  viewTitle: string;
  onFeedSubscribed: (feed: Feed) => void;
  onFeedUpdated: (feed: Feed, urlChanged: boolean) => void;
  onFolderCreated: (folder: Folder) => void;
  onFolderUpdated: (folder: Folder) => void;
  onFolderDeleted: (folder: Folder, deletion: FolderDeletion) => void;
  onUserUpdated: (user: SessionUser) => void;
  onMarkAllRead: () => Promise<unknown>;
  onImported: () => void;
};

export function ReaderDialogs({ dialog, onOpen, onClose, user, feeds, folders, viewTitle, ...handlers }: Props) {
  switch (dialog?.kind) {
    case 'feed':
    case 'editFeed': {
      const feed = dialog.kind === 'editFeed' ? dialog.feed : null;
      return <FeedModal key={feed ? `feed-${feed.id}` : 'feed'} feed={feed} folders={folders} onClose={onClose} onSubscribed={handlers.onFeedSubscribed} onUpdated={handlers.onFeedUpdated} />;
    }
    case 'folder':
    case 'editFolder': {
      const folder = dialog.kind === 'editFolder' ? dialog.folder : null;
      return <FolderModal key={folder ? `folder-${folder.id}` : 'folder'} folder={folder} onClose={onClose} onCreated={handlers.onFolderCreated} onUpdated={handlers.onFolderUpdated} />;
    }
    case 'manageFolders':
      return <FolderManager folders={folders} feeds={feeds} onRename={(folder) => onOpen({ kind: 'editFolder', folder })} onDelete={(folder) => onOpen({ kind: 'deleteFolder', folder })} onClose={onClose} />;
    case 'deleteFolder':
      return <FolderDeleteDialog folder={dialog.folder} folders={folders} feeds={feeds} onClose={onClose} onDeleted={handlers.onFolderDeleted} />;
    case 'profile':
      return <ProfileModal user={user} onClose={onClose} onUserUpdated={handlers.onUserUpdated} />;
    case 'markAllRead':
      return <MarkAllReadDialog viewTitle={viewTitle} onConfirm={handlers.onMarkAllRead} onClose={onClose} />;
    case 'importOpml':
      return <OpmlImportModal onClose={onClose} onImported={handlers.onImported} />;
    case 'shortcuts':
      return <ShortcutsDialog onClose={onClose} />;
    default:
      return null;
  }
}
