import { useTranslation } from 'react-i18next';
import './i18n';
import { useSession } from './hooks/useSession';
import { Reader } from './Reader';
import { SignIn } from './SignIn';
import { useTheme } from './useTheme';

function App() {
  const { t } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const session = useSession();

  if (session.loading) {
    return <main className="signin-screen" aria-busy="true"><div className="list-loading"><span className="loader" />{t('states.checkingSession')}</div></main>;
  }
  if (!session.user) {
    return <SignIn onLogin={session.signIn} busy={session.signingIn} theme={theme} onToggleTheme={toggleTheme} notice={session.expired ? t('signin.sessionExpired') : ''} />;
  }
  // Keyed by user so a different account never sees the previous library.
  return <Reader key={session.user.id} user={session.user} theme={theme} onToggleTheme={toggleTheme} onUserUpdated={session.setUser} onSignOut={session.signOut} onSignedOut={session.clear} />;
}

export default App;
