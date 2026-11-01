import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { api, errorMessage, folderColors, type Folder, type FolderColor } from './api';
import { Icon } from './AppChrome';
import { ErrorToast } from './ErrorToast';
import { ModalFrame } from './ModalFrame';

type Props = {
  /** The folder to rename and recolor, or null to create one. */
  folder: Folder | null;
  onClose: () => void;
  onCreated: (folder: Folder) => void;
  onUpdated: (folder: Folder) => void;
};

/** Creates a folder or edits a folder's name and color. */
export function FolderModal({ folder, onClose, onCreated, onUpdated }: Props) {
  const { t } = useTranslation();
  const [folderName, setFolderName] = useState(folder?.name ?? '');
  const [folderColor, setFolderColor] = useState<FolderColor>(folder?.color ?? 'amber');
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');
  const isCustomFolderColor = !folderColors.includes(folderColor as typeof folderColors[number]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError('');
    setSubmitting(true);
    try {
      if (folder) onUpdated(await api.updateFolder(folder.id, { name: folderName.trim(), color: folderColor }));
      else onCreated(await api.createFolder(folderName.trim(), folderColor));
    } catch (reason) {
      setFormError(errorMessage(reason, t('errors.folderCreate')));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalFrame
      icon={folder ? 'edit' : 'folder'}
      eyebrow={t('reader.library')}
      title={folder ? t('folder.edit') : t('folder.new')}
      description={t('folderModal.description')}
      footnote={t('profile.localStorageNote')}
      onClose={onClose}
    >
      <form onSubmit={(event) => void submit(event)}>
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
        <button className="primary-button modal-submit" type="submit" disabled={submitting}>{submitting ? t('states.saving') : <>{folder ? t('actions.saveChanges') : t('actions.createFolder')} <Icon name="arrow" size={16} /></>}</button>
      </form>
    </ModalFrame>
  );
}
