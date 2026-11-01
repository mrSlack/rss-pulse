import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { api, errorMessage, type SessionUser } from './api';
import { initials } from './AppChrome';
import { ErrorToast } from './ErrorToast';
import { ModalFrame } from './ModalFrame';
import { rememberUser, remembersDevice, setRemembersDevice } from './offlineData';

type Props = {
  user: SessionUser;
  onClose: () => void;
  onUserUpdated: (user: SessionUser) => void;
};

/** Edits the signed-in user's personal details and password. */
export function ProfileModal({ user, onClose, onUserUpdated }: Props) {
  const { t } = useTranslation();
  const [firstName, setFirstName] = useState(user.firstName || '');
  const [lastName, setLastName] = useState(user.lastName || '');
  const [nickname, setNickname] = useState(user.nickname || '');
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [profileError, setProfileError] = useState('');
  const [profileNotice, setProfileNotice] = useState('');
  const [passwordError, setPasswordError] = useState('');
  const [passwordNotice, setPasswordNotice] = useState('');
  const [rememberDevice, setRememberDevice] = useState(remembersDevice);

  /** Applies the offline choice at once: off deletes the stored account and articles. */
  function changeRememberDevice(remember: boolean) {
    setRememberDevice(remember);
    void setRemembersDevice(remember).then(() => (remember ? rememberUser(user) : undefined));
  }

  async function submitDetails(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setProfileError('');
    setProfileNotice('');
    setSubmitting(true);
    try {
      onUserUpdated(await api.updateProfile({ firstName: firstName.trim(), lastName: lastName.trim(), nickname: nickname.trim() }));
      setProfileNotice(t('profile.saved'));
    } catch (reason) {
      setProfileError(errorMessage(reason, t('errors.profileSave')));
    } finally {
      setSubmitting(false);
    }
  }

  async function submitPassword(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPasswordError('');
    setPasswordNotice('');
    if (newPassword !== confirmPassword) {
      setPasswordError(t('profile.passwordMismatch'));
      return;
    }
    setSubmitting(true);
    try {
      await api.changePassword(currentPassword, newPassword);
      setCurrentPassword('');
      setNewPassword('');
      setConfirmPassword('');
      setPasswordNotice(t('profile.passwordChanged'));
    } catch (reason) {
      setPasswordError(errorMessage(reason, t('errors.passwordChange')));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalFrame icon="user" eyebrow={t('profile.settings')} title={t('profile.title')} description={t('profile.description')} footnote={t('profile.passwordHelp')} onClose={onClose}>
      <div className="profile-summary-card">
        <span className="profile-dropdown-avatar">{initials(user.email)}</span>
        <span><strong>{user.email}</strong><small>{user.role === 'Administrator' ? t('roles.administrator') : t('roles.reader')}</small></span>
      </div>
      <form onSubmit={(event) => void submitDetails(event)}>
        <p className="profile-form-title">{t('profile.personalDetails')}</p>
        <label className="form-label" htmlFor="profile-first-name">{t('profile.firstName')} <span>{t('common.optional')}</span></label>
        <input className="text-input" id="profile-first-name" autoComplete="given-name" autoFocus maxLength={80} value={firstName} onChange={(event) => setFirstName(event.target.value)} />
        <label className="form-label folder-label" htmlFor="profile-last-name">{t('profile.lastName')} <span>{t('common.optional')}</span></label>
        <input className="text-input" id="profile-last-name" autoComplete="family-name" maxLength={80} value={lastName} onChange={(event) => setLastName(event.target.value)} />
        <label className="form-label folder-label" htmlFor="profile-nickname">{t('profile.nickname')} <span>{t('common.optional')}</span></label>
        <input className="text-input" id="profile-nickname" autoComplete="nickname" maxLength={40} value={nickname} onChange={(event) => setNickname(event.target.value)} />
        <ErrorToast message={profileError} onDismiss={() => setProfileError('')} />
        {profileNotice && <p className="profile-success" role="status">{profileNotice}</p>}
        <button className="primary-button modal-submit" type="submit" disabled={submitting}>{submitting ? t('states.saving') : t('profile.save')}</button>
      </form>
      <div className="profile-device">
        <p className="profile-form-title">{t('profile.offlineTitle')}</p>
        <div className="remember-device">
          <input id="profile-remember" type="checkbox" checked={rememberDevice} onChange={(event) => changeRememberDevice(event.target.checked)} aria-describedby="profile-remember-hint" />
          <label htmlFor="profile-remember">{t('profile.rememberOffline')}</label>
          <small id="profile-remember-hint">{t('profile.rememberOfflineHint')}</small>
        </div>
      </div>
      <form className="profile-password-form" onSubmit={(event) => void submitPassword(event)}>
        <p className="profile-form-title">{t('profile.changePassword')}</p>
        <label className="form-label" htmlFor="profile-current-password">{t('profile.currentPassword')}</label>
        <input className="text-input" id="profile-current-password" type="password" autoComplete="current-password" required value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} />
        <label className="form-label folder-label" htmlFor="profile-new-password">{t('profile.newPassword')}</label>
        <input className="text-input" id="profile-new-password" type="password" autoComplete="new-password" minLength={12} required value={newPassword} onChange={(event) => setNewPassword(event.target.value)} placeholder={t('profile.passwordHint')} />
        <label className="form-label folder-label" htmlFor="profile-confirm-password">{t('profile.confirmPassword')}</label>
        <input className="text-input" id="profile-confirm-password" type="password" autoComplete="new-password" minLength={12} required value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} />
        <ErrorToast message={passwordError} onDismiss={() => setPasswordError('')} />
        {passwordNotice && <p className="profile-success" role="status">{passwordNotice}</p>}
        <button className="primary-button modal-submit" type="submit" disabled={submitting}>{submitting ? t('states.saving') : t('profile.savePassword')}</button>
      </form>
    </ModalFrame>
  );
}
