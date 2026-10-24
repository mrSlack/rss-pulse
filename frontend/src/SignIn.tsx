import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { ThemeToggle } from './ThemeToggle';
import { LanguageSelector } from './LanguageSelector';
import type { Theme } from './theme';
import './i18n';
import { ErrorToast } from './ErrorToast';

type SignInProps = {
  onLogin: (email: string, password: string) => Promise<void>;
  busy?: boolean;
  theme?: Theme;
  onToggleTheme?: () => void;
};

export function SignIn({ onLogin, busy = false, theme, onToggleTheme }: SignInProps) {
  const { t } = useTranslation();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError('');
    setSubmitting(true);
    try {
      await onLogin(email.trim(), password);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('signin.unexpectedError'));
    } finally {
      setSubmitting(false);
    }
  }

  const disabled = busy || submitting;

  return (
    <main className="signin-screen">
      <ErrorToast message={error} onDismiss={() => setError('')} />
      <div className="signin-theme"><LanguageSelector />{theme && onToggleTheme && <ThemeToggle theme={theme} onToggle={onToggleTheme} />}</div>
      <div className="signin-ambient signin-ambient-one" />
      <div className="signin-ambient signin-ambient-two" />
      <section className="signin-card" aria-labelledby="signin-title">
        <a className="signin-brand" href="#signin" aria-label={t('brand.name')}>
          <span className="brand-mark"><svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"><path d="M5 19a1 1 0 1 0 0-2 1 1 0 0 0 0 2Z" /><path d="M4 11a9 9 0 0 1 9 9M4 5a15 15 0 0 1 15 15" /></svg></span>
          <span className="brand-name">Pulse<span>.</span></span>
        </a>
        <p className="signin-kicker">{t('signin.kicker')}</p>
        <h1 id="signin-title">{t('signin.title')}</h1>
        <p className="signin-copy">{t('signin.description')}</p>
        <form className="signin-form" onSubmit={(event) => void submit(event)}>
          <label htmlFor="signin-email">{t('signin.email')}</label>
          <input id="signin-email" type="email" autoComplete="username" autoFocus required value={email} onChange={(event) => setEmail(event.target.value)} placeholder="you@example.com" />
          <label htmlFor="signin-password">{t('signin.password')}</label>
          <input id="signin-password" type="password" autoComplete="current-password" required value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t('signin.passwordPlaceholder')} />
          <button className="signin-submit" type="submit" disabled={disabled}>{disabled ? t('signin.submitting') : t('signin.submit')}<span aria-hidden="true">↗</span></button>
        </form>
        <div className="signin-foot"><span />{t('signin.footer')}</div>
      </section>
      <p className="signin-caption">{t('signin.caption')}</p>
    </main>
  );
}
