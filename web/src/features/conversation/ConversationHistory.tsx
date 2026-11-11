import { SearchIcon, XIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { Link, useMatch } from "react-router-dom";
import { useConversations } from "./ConversationsProvider";
import { groupByRecency, useLocalDay } from "./recency";

/** Past this many conversations, a search field helps find older ones. */
const SEARCH_THRESHOLD = 12;

/**
 * Recent conversations, grouped under date headings. The full sidebar shows the
 * list in place; the rail opens it as a drawer with its own Close.
 */
export function ConversationHistory({ id, open, onNavigate, onClose }: { id: string; open: boolean; onNavigate?: () => void; onClose?: () => void }) {
  const { conversations, error, loading, refresh } = useConversations();
  const current = useMatch("/conversations/:id")?.params.id;
  const [query, setQuery] = useState("");
  const list = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const needle = query.trim().toLocaleLowerCase();
  const day = useLocalDay();
  const groups = useMemo(() => groupByRecency(
    needle ? conversations.filter(conversation => label(conversation.preview).toLocaleLowerCase().includes(needle)) : conversations,
    new Date(day),
  ), [conversations, needle, day]);
  // Each search reads its results from the top.
  useEffect(() => { if (list.current) list.current.scrollTop = 0; }, [needle]);
  // A long list can hold the open conversation below the fold; bring it into view.
  useEffect(() => {
    list.current?.querySelector<HTMLElement>("a[aria-current=page]")?.scrollIntoView?.({ block: "nearest" });
  }, [current, open, conversations.length]);
  const searchable = conversations.length > SEARCH_THRESHOLD || query !== "";
  return <section id={id} className={`conversation-history${open ? " open" : ""}`} aria-labelledby={`${id}-title`}>
    <div className="history-header">
      <h2 id={`${id}-title`} tabIndex={-1}>Recent</h2>
      {onClose && <button type="button" className="icon-button" aria-label="Close recent conversations" onClick={onClose}><XIcon aria-hidden="true" /></button>}
    </div>
    {searchable && <div className="history-search">
      <SearchIcon aria-hidden="true" />
      <input ref={search} type="search" aria-label="Search conversations" placeholder="Search conversations" value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => {
        // Escape clears a search first; only an empty field lets it close the drawer.
        if (event.key !== "Escape" || !query) return;
        event.preventDefault();
        event.stopPropagation();
        setQuery("");
      }} />
      {query && <button type="button" aria-label="Clear search" onClick={() => { setQuery(""); search.current?.focus(); }}><XIcon aria-hidden="true" /></button>}
    </div>}
    {error ? <p className="history-note" role="alert">Could not load conversations. <button onClick={refresh}>Retry</button></p>
      : !loading && conversations.length === 0 ? <p className="history-note">Conversations you start appear here.</p>
      : needle && groups.length === 0 ? <p className="history-note" role="status">No conversations match “{query.trim()}”.</p> : null}
    {groups.length > 0 && <div ref={list} className="history-list">{groups.map(group => <div key={group.label} className="history-group">
      <h3>{group.label}</h3>
      <ul>{group.items.map(conversation => <li key={conversation.id}>
        <Link to={`/conversations/${encodeURIComponent(conversation.id)}`} aria-current={current === conversation.id ? "page" : undefined} onClick={onNavigate} onMouseEnter={showTruncated}>{label(conversation.preview)}</Link>
      </li>)}</ul>
    </div>)}</div>}
  </section>;
}

function label(preview: string) { return preview || "Empty conversation"; }

/** Only a truncated row repeats its preview as a tooltip. */
function showTruncated(event: MouseEvent<HTMLAnchorElement>) {
  const row = event.currentTarget;
  row.title = row.scrollWidth > row.clientWidth ? row.textContent ?? "" : "";
}
