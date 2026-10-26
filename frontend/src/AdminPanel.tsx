import { useTranslation } from 'react-i18next';
import './i18n';
import { useEffect, useState, type FormEvent } from 'react';
import { api, type AdminSettings, type FeedRequest, type UserRole } from './api';
import { ErrorToast } from './ErrorToast';

const intervalChoices = [5, 10, 15, 30, 60, 120, 360, 720, 1440];

function intervalOptions(current: number | null) {
  return [...new Set([...intervalChoices, ...(current === null ? [] : [current])])].sort((a, b) => a - b);
}

export function AdminPanel() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<AdminSettings | null>(null);
  const [feedRequests, setFeedRequests] = useState<FeedRequest[]>([]);
  const [refreshInterval, setRefreshInterval] = useState('15');
  const [sharedTTL, setSharedTTL] = useState('15');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<UserRole>('Reader');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  async function loadSettings() {
    const [next, requests] = await Promise.all([api.getAdminSettings(), api.getFeedRequests()]);
    setSettings(next);
    setFeedRequests(requests);
    setRefreshInterval(String(next.refreshIntervalMinutes));
    setSharedTTL(String(next.sharedFeedTTLMinutes));
  }

  useEffect(() => {
    let active = true;
    Promise.all([api.getAdminSettings(), api.getFeedRequests()])
      .then(([next, requests]) => {
        if (!active) return;
        setSettings(next);
        setFeedRequests(requests);
        setRefreshInterval(String(next.refreshIntervalMinutes));
        setSharedTTL(String(next.sharedFeedTTLMinutes));
      })
      .catch((reason: unknown) => { if (active) setError(reason instanceof Error ? reason.message : t('errors.adminLoad')); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, []);

  async function createAccount(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError('');
    setNotice('');
    setSaving(true);
    try {
      await api.createUser({ email: email.trim(), password, role });
      setEmail('');
      setPassword('');
      await loadSettings();
      setNotice(t('admin.created'));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.userCreate'));
    } finally {
      setSaving(false);
    }
  }

  async function decideFeedRequest(request: FeedRequest, approve: boolean) {
    setError('');
    setNotice('');
    try {
      if (approve) await api.approveFeedRequest(request.id);
      else await api.rejectFeedRequest(request.id);
      setFeedRequests((current) => current.filter((item) => item.id !== request.id));
      setNotice(approve ? t('admin.feedApproved') : t('admin.feedRejected'));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.feedRequest'));
    }
  }

  async function saveSettings(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError('');
    setNotice('');
    const refreshIntervalMinutes = Number(refreshInterval);
    const sharedFeedTTLMinutes = Number(sharedTTL);
    if (![refreshIntervalMinutes, sharedFeedTTLMinutes].every((value) => Number.isInteger(value) && value >= 5 && value <= 1440)) {
      setError(t('admin.invalidIntervals'));
      return;
    }
    setSaving(true);
    try {
      await api.updateAdminSettings({ refreshIntervalMinutes, sharedFeedTTLMinutes });
      await loadSettings();
      setNotice(t('admin.settingsSaved'));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.settingsSave'));
    } finally {
      setSaving(false);
    }
  }

  async function updateRole(userID: number, nextRole: UserRole) {
    if (!settings) return;
    setError('');
    setNotice('');
    try {
      const updated = await api.updateUser(userID, { role: nextRole });
      setSettings({ ...settings, users: settings.users.map((user) => user.id === userID ? updated : user) });
      setNotice(t('admin.roleUpdated'));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.roleUpdate'));
      await loadSettings().catch(() => undefined);
    }
  }

  async function updateUserInterval(userID: number, value: string) {
    if (!settings) return;
    setError('');
    setNotice('');
    const refreshIntervalMinutes = value === '' ? null : Number(value);
    try {
      const updated = await api.updateUser(userID, { refreshIntervalMinutes });
      setSettings({ ...settings, users: settings.users.map((user) => user.id === userID ? updated : user) });
      setNotice(refreshIntervalMinutes === null ? t('admin.inheritedInterval') : t('admin.userIntervalSaved'));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('errors.userIntervalSave'));
      await loadSettings().catch(() => undefined);
    }
  }

  return (
    <section className="admin-panel" aria-labelledby="admin-title">
      <header className="admin-heading">
        <div>
          <p className="admin-eyebrow"><span />{t('admin.eyebrow')}</p>
          <h1 id="admin-title">{t('admin.title')}</h1>
          <p>{t('admin.description')}</p>
        </div>
        <span className="admin-shield" aria-hidden="true">✳</span>
      </header>

      <ErrorToast message={error} onDismiss={() => setError('')} />
      {notice && <div className="admin-notice" role="status">{notice}</div>}
      {loading ? <div className="admin-loading">{t('states.loadingSettings')}</div> : <>
        <div className="admin-settings-grid">
          <form className="admin-card" onSubmit={(event) => void saveSettings(event)}>
            <div className="admin-card-heading"><span className="admin-card-icon">◷</span><div><h2>{t('admin.refreshTitle')}</h2><p>{t('admin.refreshDescription')}</p></div></div>
            <div className="admin-interval-grid">
              <div className="admin-setting-block">
                <div className="admin-setting-heading">
                  <label className="admin-field-label" htmlFor="scheduler-interval">{t('admin.schedulerInterval')}</label>
                  <span className="admin-setting-status pending">{t('admin.pending')}</span>
                </div>
                <div className="admin-number-wrap"><input id="scheduler-interval" type="number" min="5" max="1440" step="1" value={refreshInterval} onChange={(event) => setRefreshInterval(event.target.value)} /><span>{t('common.minutesShort')}</span></div>
                <p className="admin-field-note">{t('admin.defaultForUsers')}</p>
              </div>
              <div className="admin-setting-block">
                <div className="admin-setting-heading">
                  <label className="admin-field-label" htmlFor="shared-ttl">{t('admin.sharedTTL')}</label>
                  <span className="admin-setting-status active">{t('admin.active')}</span>
                </div>
                <div className="admin-number-wrap"><input id="shared-ttl" type="number" min="5" max="1440" step="1" value={sharedTTL} onChange={(event) => setSharedTTL(event.target.value)} /><span>{t('common.minutesShort')}</span></div>
                <p className="admin-field-note">{t('admin.ttlDescription')}</p>
              </div>
            </div>
            <button className="primary-button admin-save" type="submit" disabled={saving}>{saving ? t('states.saving') : t('admin.saveIntervals')}</button>
          </form>

          <form className="admin-card account-card" onSubmit={(event) => void createAccount(event)}>
            <div className="admin-card-heading"><span className="admin-card-icon account-icon">＋</span><div><h2>{t('admin.newUser')}</h2><p>{t('admin.newUserDescription')}</p></div></div>
            <label className="admin-field-label" htmlFor="new-user-email">{t('admin.newUserEmail')}</label>
            <input className="admin-text-input" id="new-user-email" type="email" autoComplete="off" required value={email} onChange={(event) => setEmail(event.target.value)} placeholder="reader@example.com" />
            <label className="admin-field-label admin-field-spaced" htmlFor="new-user-password">{t('admin.initialPassword')}</label>
            <input className="admin-text-input" id="new-user-password" type="password" autoComplete="new-password" minLength={12} required value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t('profile.passwordHint')} />
            <label className="admin-field-label admin-field-spaced" htmlFor="new-user-role">{t('admin.newUserRole')}</label>
            <select className="admin-select" id="new-user-role" value={role} onChange={(event) => setRole(event.target.value as UserRole)}><option value="Reader">{t('roles.reader')}</option><option value="Administrator">{t('roles.administrator')}</option></select>
            <p className="admin-field-note">{t('admin.passwordNotice')}</p>
            <button className="primary-button admin-save" type="submit" disabled={saving}>{saving ? t('states.creating') : t('admin.createUser')}</button>
          </form>
        </div>

        <section className="admin-card admin-users-card" aria-labelledby="feed-requests-title">
          <div className="admin-card-heading"><span className="admin-card-icon users-icon">⌁</span><div><h2 id="feed-requests-title">{t('admin.feedRequests')}</h2><p>{t('admin.feedRequestsDescription')}</p></div><span className="admin-user-count">{feedRequests.length}</span></div>
          {feedRequests.length === 0 ? <p className="admin-schedule-notice">{t('admin.noFeedRequests')}</p> : <div className="admin-table-wrap"><table className="admin-table"><thead><tr><th scope="col">{t('admin.feedRequestUrl')}</th><th scope="col">{t('admin.feedRequestUser')}</th><th scope="col">{t('admin.feedRequestFolder')}</th><th scope="col">{t('admin.feedRequestActions')}</th></tr></thead><tbody>
            {feedRequests.map((request) => <tr key={request.id}>
              <td><a href={request.url} target="_blank" rel="noreferrer">{request.url}</a></td>
              <td>{request.requestedBy}</td>
              <td>{request.folderName || t('folder.none')}</td>
              <td><div className="admin-request-actions"><button className="primary-button" type="button" onClick={() => void decideFeedRequest(request, true)}>{t('admin.approveFeed')}</button><button className="secondary-button" type="button" onClick={() => void decideFeedRequest(request, false)}>{t('admin.rejectFeed')}</button></div></td>
            </tr>)}
          </tbody></table></div>}
        </section>

        <section className="admin-card admin-users-card" aria-labelledby="users-title">
          <div className="admin-card-heading"><span className="admin-card-icon users-icon">◎</span><div><h2 id="users-title">{t('admin.users')}</h2><p>{t('admin.usersDescription')}</p></div><span className="admin-user-count" aria-label={t('admin.userCount', { count: settings?.users.length || 0 })}>{settings?.users.length || 0}</span></div>
          <div className="admin-table-wrap"><table className="admin-table"><thead><tr><th scope="col">{t('admin.userColumn')}</th><th scope="col">{t('admin.roleColumn')}</th><th scope="col">{t('admin.intervalColumn')}</th></tr></thead><tbody>
            {settings?.users.map((user) => <tr key={user.id}>
              <td><span className="admin-user-avatar">{user.email.slice(0, 1).toUpperCase()}</span><span className="admin-user-email">{user.email}</span></td>
              <td><label className="visually-hidden" htmlFor={`role-${user.id}`}>{t('admin.roleFor', { email: user.email })}</label><select id={`role-${user.id}`} className="admin-select table-select" value={user.role} onChange={(event) => void updateRole(user.id, event.target.value as UserRole)}><option value="Reader">{t('roles.reader')}</option><option value="Administrator">{t('roles.administrator')}</option></select></td>
              <td><label className="visually-hidden" htmlFor={`interval-${user.id}`}>{t('admin.intervalFor', { email: user.email })}</label><select id={`interval-${user.id}`} className="admin-select table-select" value={user.refreshIntervalMinutes === null ? '' : String(user.refreshIntervalMinutes)} onChange={(event) => void updateUserInterval(user.id, event.target.value)}><option value="">{t('admin.defaultInterval', { minutes: settings.refreshIntervalMinutes })}</option>{intervalOptions(user.refreshIntervalMinutes).map((value) => <option key={value} value={value}>{t('admin.minutes', { count: value })}</option>)}</select></td>
            </tr>)}
          </tbody></table></div>
          <p className="admin-schedule-notice">{t('admin.scheduleNotice')}</p>
        </section>
      </>}
    </section>
  );
}
