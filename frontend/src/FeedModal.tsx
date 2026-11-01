import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { api, errorMessage, type Feed, type Folder } from './api';
import { Icon } from './AppChrome';
import { ErrorToast } from './ErrorToast';
import { FolderPicker } from './FolderPicker';
import { ModalFrame } from './ModalFrame';

type Props = {
  /** The subscription to edit, or null to add a new feed. */
  feed: Feed | null;
  folders: Folder[];
  onClose: () => void;
  onSubscribed: (feed: Feed) => void;
  onUpdated: (feed: Feed, urlChanged: boolean) => void;
};

/** Adds a feed (or requests its approval) or edits a subscription. */
export function FeedModal({ feed, folders, onClose, onSubscribed, onUpdated }: Props) {
  const { t } = useTranslation();
  const editing = feed !== null;
  const [feedUrl, setFeedUrl] = useState(feed?.url ?? '');
  const [feedTitle, setFeedTitle] = useState(feed?.title ?? '');
  const [targetFolder, setTargetFolder] = useState(feed?.folderId ? String(feed.folderId) : '');
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');
  const [formNotice, setFormNotice] = useState('');

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError('');
    setFormNotice('');
    setSubmitting(true);
    const folderId = targetFolder ? Number(targetFolder) : null;
    try {
      const result = feed
        ? await api.updateFeed(feed.id, { title: feedTitle.trim(), url: feedUrl.trim(), folderId })
        : await api.createFeed(feedUrl.trim(), folderId);
      if ('requestId' in result) {
        setFormNotice(t('feedModal.requestedApproval'));
        if (!feed) setFeedUrl('');
        return;
      }
      if (feed) onUpdated(result, result.url !== feed.url);
      else onSubscribed(result);
    } catch (reason) {
      setFormError(errorMessage(reason, feed ? t('errors.feedUpdate') : t('errors.feedAdd')));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalFrame
      icon={editing ? 'edit' : 'rss'}
      eyebrow={t('reader.library')}
      title={editing ? t('feedModal.editTitle') : t('actions.addSource')}
      description={editing ? t('feedModal.editDescription') : t('feedModal.description')}
      footnote={editing ? t('feedModal.urlChangeNote') : t('profile.localStorageNote')}
      onClose={onClose}
    >
      <form className="feed-form" onSubmit={(event) => void submit(event)}>
        {editing && <>
          <div className="form-field">
            <label className="form-label" htmlFor="feed-title">{t('feedModal.title')}</label>
            <input className="text-input" id="feed-title" autoFocus maxLength={120} required value={feedTitle} onChange={(event) => setFeedTitle(event.target.value)} />
          </div>
        </>}
        <div className="form-field">
          <label className="form-label" htmlFor="feed-url">{t('feedModal.url')}</label>
          <div className="url-input-wrap"><Icon name="rss" size={17} /><input id="feed-url" type="url" required autoFocus={!editing} value={feedUrl} onChange={(event) => setFeedUrl(event.target.value)} placeholder="https://example.com/feed.xml" /></div>
        </div>
        <div className="form-field">
          <label className="form-label" id="feed-folder-label" htmlFor="feed-folder">{t('folder.label')} <span>{t('common.optional')}</span></label>
          <FolderPicker id="feed-folder" labelId="feed-folder-label" folders={folders} value={targetFolder} noneLabel={t('folder.none')} onChange={setTargetFolder} />
        </div>
        <ErrorToast message={formError} onDismiss={() => setFormError('')} />
        {formNotice && <p className="profile-success" role="status">{formNotice}</p>}
        <button className="primary-button modal-submit" type="submit" disabled={submitting}>{submitting ? <><span className="button-spinner" />{editing ? t('states.saving') : t('states.checkingFeed')}</> : editing ? <>{t('actions.saveChanges')}<Icon name="arrow" size={16} /></> : <>{t('actions.subscribe')}<Icon name="arrow" size={16} /></>}</button>
      </form>
    </ModalFrame>
  );
}
