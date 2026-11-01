// Applies the saved or system colour theme before the first paint. It is an
// external file (not inline) so the Content-Security-Policy can stay at
// script-src 'self'. Load it synchronously in <head>, without defer or async.
(() => {
  let prefersDark = false;
  let savedTheme = null;
  try { prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches; } catch { /* keep light */ }
  try { savedTheme = window.localStorage.getItem('pulse-theme'); } catch { /* storage blocked */ }
  document.documentElement.dataset.theme = savedTheme === 'light' || savedTheme === 'dark'
    ? savedTheme
    : prefersDark ? 'dark' : 'light';
})();
