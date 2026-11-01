import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { api, errorMessage, type Feed, type Folder } from './api';
import { Icon } from './AppChrome';
import { ErrorToast } from './ErrorToast';
import type { FolderDeletion } from './hooks/useLibrary';
import { ModalFrame } from './ModalFrame';

type ManagerProps = {
  folders: Folder[];
  feeds: Feed[];
  onRename: (folder: Folder) => void;
  onDelete: (folder: Folder) => void;
  onClose: () => void;
};

/** Lists the user's folders with rename and delete actions. */
export function FolderManager({ folders, feeds, onRename, onDelete, onClose }: ManagerProps) {
  const { t } = useTranslation();
  return (
    <ModalFrame icon="settings" eyebrow={t('folder.manage')} title={t('folder.manage')} description={t('folder.manageDescription')} onClose={onClose}>
      <div className="folder-management-list">
        {folders.length === 0 ? <p className="folder-management-empty">{t('folder.manageEmpty')}</p> : folders.map((folder) => <div className="folder-management-row" key={folder.id}>
          <div className="folder-management-copy"><strong>{folder.name}</strong><span>{t('folder.sourceCount', { count: feeds.filter((feed) => feed.folderId === folder.id).length })}</span></div>
          <button className="folder-management-action" type="button" aria-label={t('folder.renameFor', { name: folder.name })} title={t('folder.rename')} onClick={() => onRename(folder)}><Icon name="edit" size={16} /></button>
          <button className="folder-management-action folder-management-delete" type="button" aria-label={t('folder.deleteFor', { name: folder.name })} title={t('folder.delete')} onClick={() => onDelete(folder)}><Icon name="trash" size={16} /></button>
        </div>)}
      </div>
    </ModalFrame>
  );
}

type DeleteProps = {
  folder: Folder;
  folders: Folder[];
  feeds: Feed[];
  onClose: () => void;
  onDeleted: (folder: Folder, deletion: FolderDeletion) => void;
};

/** Confirms a folder deletion and asks what happens to its subscriptions. */
export function FolderDeleteDialog({ folder, folders, feeds, onClose, onDeleted }: DeleteProps) {
  const { t } = useTranslation();
  const [action, setAction] = useState<FolderDeletion['action']>('delete_sources');
  const [target, setTarget] = useState(() => String(folders.find((candidate) => candidate.id !== folder.id)?.id ?? ''));
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState('');
  const sourceCount = feeds.filter((feed) => feed.folderId === folder.id).length;
  const otherFolders = folders.filter((candidate) => candidate.id !== folder.id);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError('');
    setBusy(true);
    try {
      const deletion: FolderDeletion = action === 'delete_sources' ? { action } : { action, targetFolderId: Number(target) };
      await api.deleteFolder(folder.id, deletion);
      onDeleted(folder, deletion);
    } catch (reason) {
      setFormError(errorMessage(reason, t('errors.folderDelete')));
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalFrame
      icon="trash"
      eyebrow={t('folder.manage')}
      title={t('folder.deleteTitle')}
      description={sourceCount > 0 ? t('folder.deleteWarning', { name: folder.name, count: sourceCount }) : t('folder.deleteEmptyWarning', { name: folder.name })}
      onClose={onClose}
    >
      <form className="folder-delete-form" onSubmit={(event) => void submit(event)}>
        {sourceCount > 0 && <fieldset className="folder-delete-options">
          <legend className="form-label">{t('folder.deleteChoice')}</legend>
          <label className="folder-delete-option"><input type="radio" name="folder-delete-action" checked={action === 'delete_sources'} onChange={() => setAction('delete_sources')} /><span><strong>{t('folder.deleteSources')}</strong><small>{t('folder.deleteSourcesDescription')}</small></span></label>
          {otherFolders.length > 0 && <label className="folder-delete-option"><input type="radio" name="folder-delete-action" checked={action === 'move_sources'} onChange={() => setAction('move_sources')} /><span><strong>{t('folder.moveSources')}</strong><small>{t('folder.moveSourcesDescription')}</small></span></label>}
          {action === 'move_sources' && <div className="folder-delete-target"><label className="form-label" htmlFor="folder-delete-target">{t('folder.moveTo')}</label><select id="folder-delete-target" required value={target} onChange={(event) => setTarget(event.target.value)}>{otherFolders.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.name}</option>)}</select></div>}
        </fieldset>}
        <ErrorToast message={formError} onDismiss={() => setFormError('')} />
        <button className="folder-delete-confirm" type="submit" disabled={busy}>{busy ? t('folder.deleting') : t('folder.confirmDelete')}</button>
      </form>
    </ModalFrame>
  );
}
