export function ArticleTitle({ href, title, onOpenLocal }: { href: string; title: string; onOpenLocal: () => void }) {
  return (
    <a
      className="article-title-link"
      href={href}
      rel="noreferrer"
      onClick={(event) => {
        if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        event.preventDefault();
        onOpenLocal();
      }}
    >
      {title}
    </a>
  );
}
