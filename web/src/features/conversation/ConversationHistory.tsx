import { SearchIcon, XIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { Link, useMatch } from "react-router-dom";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { cn } from "../../lib/utils";
import { useConversations } from "./ConversationsProvider";
import { groupByRecency, useLocalDay } from "./recency";

/** Past this many conversations, a search field helps find older ones. */
const SEARCH_THRESHOLD = 12;

/**
 * Recent conversations, grouped under date headings. The full sidebar shows the
 * list in place; the rail opens it as a drawer with its own Close.
 */
export function ConversationHistory({ id, open, onNavigate, onClose, classNames }: { id: string; open: boolean; onNavigate?: () => void; onClose?: () => void; classNames?: { root?: string; header?: string; close?: string } }) {
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
  return <section id={id} className={cn("conversation-history flex min-h-0 flex-1 flex-col border-t border-border pt-5 [--history-bg:var(--gray-0)] [--history-field:var(--gray-1)]", classNames?.root)} aria-labelledby={`${id}-title`}>
    <div className={cn("flex min-h-5 items-center justify-between gap-2 px-3 pb-2", classNames?.header)}>
      <h2 className="text-[13px] leading-5 font-semibold text-muted-foreground" id={`${id}-title`} tabIndex={-1}>Recent</h2>
      {onClose && <Button variant="ghost" size="icon-touch" type="button" className={classNames?.close} aria-label="Close recent conversations" onClick={onClose}><XIcon aria-hidden="true" /></Button>}
    </div>
    {searchable && <div className="history-search mb-2 flex min-h-9 shrink-0 items-center gap-2 rounded-md border border-border bg-[var(--history-field)] ps-3 pe-1 text-muted-foreground focus-within:border-ring pointer-coarse:min-h-11">
      <SearchIcon className="size-4 shrink-0" aria-hidden="true" />
      <Input size="auto" className="h-[34px] min-w-0 flex-1 appearance-none rounded-none border-0 bg-transparent p-0 text-label text-foreground caret-violet placeholder:text-muted-foreground placeholder:opacity-100 focus-visible:outline-none pointer-coarse:h-[42px] pointer-coarse:text-body [&::-webkit-search-cancel-button]:appearance-none" ref={search} type="search" aria-label="Search conversations" placeholder="Search conversations" value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => {
        // Escape clears a search first; only an empty field lets it close the drawer.
        if (event.key !== "Escape" || !query) return;
        event.preventDefault();
        event.stopPropagation();
        setQuery("");
      }} />
      {query && <Button variant="ghost" size="icon" className="pointer-coarse:size-11" type="button" aria-label="Clear search" onClick={() => { setQuery(""); search.current?.focus(); }}><XIcon aria-hidden="true" /></Button>}
    </div>}
    {error ? <p className="px-3 text-[13px] leading-normal text-muted-foreground" role="alert">Could not load conversations. <Button variant="link" onClick={refresh}>Retry</Button></p>
      : !loading && conversations.length === 0 ? <p className="px-3 text-[13px] leading-normal text-muted-foreground">Conversations you start appear here.</p>
      : needle && groups.length === 0 ? <p className="px-3 text-[13px] leading-normal text-muted-foreground" role="status">No conversations match “{query.trim()}”.</p> : null}
    {groups.length > 0 && <div ref={list} className="min-h-0 flex-1 overflow-y-auto overscroll-contain [scrollbar-color:var(--gray-5)_transparent] [scrollbar-width:thin]">{groups.map(group => <div key={group.label} className="not-first:mt-3">
      <h3 className="sticky top-0 z-1 bg-[var(--history-bg)] px-3 pt-1.5 pb-1 text-caption leading-[18px] font-medium text-gray-7">{group.label}</h3>
      <ul className="grid list-none grid-cols-[minmax(0,1fr)] gap-0.5">{group.items.map(conversation => <li key={conversation.id}>
        <Link className="block scroll-mt-7 truncate rounded-md px-3 py-2 text-label leading-5 text-muted-foreground no-underline hover:bg-gray-2 hover:text-foreground aria-[current=page]:bg-gray-3 aria-[current=page]:text-foreground pointer-coarse:py-3" to={`/conversations/${encodeURIComponent(conversation.id)}`} aria-current={current === conversation.id ? "page" : undefined} onClick={onNavigate} onMouseEnter={showTruncated}>{label(conversation.preview)}</Link>
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
