import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { api, errorMessage, type OpmlImportResult } from './api';
import { Icon } from './AppChrome';
import { ErrorToast } from './ErrorToast';
import { ModalFrame } from './ModalFrame';

const maxOpmlBytes = 1024 * 1024;

type Props = {
  onClose: () => void;
  /** Called after an import changed the library. */
  onImported: () => void;
};

/** Imports subscriptions from an OPML file and summarizes the outcome. */
export function OpmlImportModal({ onClose, onImported }: Props) {
  const { t } = useTranslation();
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState<OpmlImportResult | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!file) return;
    setError('');
    setResult(null);
    if (file.size > maxOpmlBytes) {
      setError(t('apiErrors.opml.too_large'));
      return;
    }
    setBusy(true);
    try {
      const imported = await api.importOpml(file);
      setResult(imported);
      if (imported.subscribed > 0 || imported.requested > 0) onImported();
    } catch (reason) {
      setError(errorMessage(reason, t('errors.opmlImport')));
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalFrame icon="upload" eyebrow={t('reader.library')} title={t('opml.importTitle')} description={t('opml.importDescription')} footnote={t('opml.importNote')} onClose={onClose}>
      <form onSubmit={(event) => void submit(event)}>
        <label className="form-label" htmlFor="opml-file">{t('opml.file')}</label>
        <input className="opml-file-input" id="opml-file" type="file" accept=".opml,.xml,text/x-opml,application/xml,text/xml" onChange={(event) => { setFile(event.target.files?.[0] ?? null); setResult(null); }} />
        <ErrorToast message={error} onDismiss={() => setError('')} />
        <button className="primary-button modal-submit" type="submit" disabled={busy || !file}>{busy ? <><span className="button-spinner" />{t('opml.importing')}</> : <>{t('opml.importAction')}<Icon name="arrow" size={16} /></>}</button>
      </form>
      {result && <div className="opml-result" role="status">
        <p>{t('opml.resultSubscribed', { count: result.subscribed })}</p>
        <p>{t('opml.resultRequested', { count: result.requested })}</p>
        <p>{t('opml.resultSkipped', { count: result.skipped })}</p>
        {result.failed.length > 0 && <>
          <p>{t('opml.resultFailed', { count: result.failed.length })}</p>
          <ul>{result.failed.map((failure, index) => <li key={`${index}-${failure.url}`}>{failure.url} — {t(`apiErrors.${failure.reason}`, { defaultValue: failure.reason })}</li>)}</ul>
        </>}
      </div>}
    </ModalFrame>
  );
}
