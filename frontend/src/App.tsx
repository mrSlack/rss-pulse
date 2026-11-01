import { useTranslation } from 'react-i18next';
import { AppStatus } from './AppStatus';
import './i18n';
import { useSession } from './hooks/useSession';
import { Reader } from './Reader';
import { SignIn } from './SignIn';
import { useTheme } from './useTheme';

function App() {
  const { t } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const session = useSession();

  let content;
  if (session.loading) {
    content = <main className="signin-screen" aria-busy="true"><div className="list-loading"><span className="loader" />{t('states.checkingSession')}</div></main>;
  } else if (!session.user) {
    content = <SignIn onLogin={session.signIn} busy={session.signingIn} theme={theme} onToggleTheme={toggleTheme} notice={session.expired ? t('signin.sessionExpired') : ''} />;
  } else {
    // Keyed by user so a different account never sees the previous library.
    content = <Reader key={session.user.id} user={session.user} theme={theme} onToggleTheme={toggleTheme} onUserUpdated={session.setUser} onSignOut={session.signOut} onSignedOut={session.clear} />;
  }

  return <>
    {content}
    <AppStatus signedIn={Boolean(session.user)} />
  </>;
}

export default App;
