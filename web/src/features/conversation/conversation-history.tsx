import { SearchIcon, XIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { MouseEvent, ReactNode } from "react";
import { Link, useMatch } from "react-router-dom";

import { Button } from "../../components/button";
import { Input } from "../../components/input";
import { cn } from "../../lib/utils";
import { useConversations } from "./conversations-provider";
import { groupByRecency, useLocalDay } from "./recency";

/** Past this many conversations, a search field helps find older ones. */
const SEARCH_THRESHOLD = 12;

const label = (preview: string) => preview || "Empty conversation";

/** Only a truncated row repeats its preview as a tooltip. */
const showTruncated = (event: MouseEvent<HTMLAnchorElement>) => {
  const row = event.currentTarget;
  row.title = row.scrollWidth > row.clientWidth ? (row.textContent ?? "") : "";
};

/**
 * Recent conversations, grouped under date headings. The full sidebar shows the
 * list in place; the rail opens it as a drawer with its own Close.
 */
export const ConversationHistory = ({
  id,
  open,
  onNavigate,
  onClose,
  classNames,
}: {
  id: string;
  open: boolean;
  onNavigate?: () => void;
  onClose?: () => void;
  classNames?: { root?: string; header?: string; close?: string };
}) => {
  const { conversations, error, loading, refresh } = useConversations();
  const current = useMatch("/conversations/:id")?.params.id;
  const [query, setQuery] = useState("");
  const list = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const needle = query.trim().toLocaleLowerCase();
  const day = useLocalDay();
  const groups = useMemo(
    () =>
      groupByRecency(
        needle
          ? conversations.filter((conversation) =>
              label(conversation.preview).toLocaleLowerCase().includes(needle)
            )
          : conversations,
        new Date(day)
      ),
    [conversations, needle, day]
  );
  // Each search reads its results from the top.
  const previousNeedle = useRef(needle);
  useEffect(() => {
    if (previousNeedle.current === needle) {
      return;
    }
    previousNeedle.current = needle;
    if (list.current) {
      list.current.scrollTop = 0;
    }
  }, [needle]);
  // A long list can hold the open conversation below the fold; bring it into view.
  const lastReveal = useRef<{
    current?: string;
    open: boolean;
    count: number;
  }>({ open: false, count: -1 });
  useEffect(() => {
    const previous = lastReveal.current;
    if (
      previous.current === current &&
      previous.open === open &&
      previous.count === conversations.length
    ) {
      return;
    }
    lastReveal.current = { current, open, count: conversations.length };
    list.current
      ?.querySelector<HTMLElement>("a[aria-current=page]")
      ?.scrollIntoView?.({ block: "nearest" });
  });
  const searchable = conversations.length > SEARCH_THRESHOLD || query !== "";
  let notice: ReactNode = null;
  if (error) {
    notice = (
      <p
        className="text-label text-muted-foreground px-3 leading-normal"
        role="alert"
      >
        Could not load conversations.{" "}
        <Button variant="link" size="touch" onClick={refresh}>
          Retry
        </Button>
      </p>
    );
  } else if (!loading && conversations.length === 0) {
    notice = (
      <p className="text-label text-muted-foreground px-3 leading-normal">
        Conversations you start appear here.
      </p>
    );
  } else if (needle && groups.length === 0) {
    notice = (
      <output className="text-label text-muted-foreground px-3 leading-normal">
        No conversations match “{query.trim()}”.
      </output>
    );
  }
  return (
    <section
      id={id}
      className={cn(
        "border-border flex min-h-0 flex-1 flex-col border-t pt-5 [--history-bg:var(--gray-0)]",
        classNames?.root
      )}
      aria-labelledby={`${id}-title`}
    >
      <div
        className={cn(
          "flex min-h-5 items-center justify-between gap-2 px-3 pb-2",
          classNames?.header
        )}
      >
        <h2
          className="text-label text-muted-foreground leading-5 font-semibold"
          id={`${id}-title`}
          tabIndex={-1}
        >
          Recent
        </h2>
        {onClose && (
          <Button
            variant="ghost"
            size="icon-touch"
            type="button"
            className={classNames?.close}
            aria-label="Close recent conversations"
            onClick={onClose}
          >
            <XIcon aria-hidden="true" />
          </Button>
        )}
      </div>
      {searchable && (
        <div
          data-slot="history-search"
          className="mb-2 flex min-h-11 shrink-0 items-center gap-1"
        >
          <div className="relative min-w-0 flex-1">
            <SearchIcon
              className="text-muted-foreground pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2"
              aria-hidden="true"
            />
            <Input
              size="auto"
              className="ps-9 [&::-webkit-search-cancel-button]:appearance-none"
              ref={search}
              type="search"
              aria-label="Search conversations"
              placeholder="Search conversations"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                // Escape clears a search first; only an empty field lets it close the drawer.
                if (event.key !== "Escape" || !query) {
                  return;
                }
                event.preventDefault();
                event.stopPropagation();
                setQuery("");
              }}
            />
          </div>
          {query && (
            <Button
              variant="ghost"
              size="icon-touch"
              type="button"
              aria-label="Clear search"
              onClick={() => {
                setQuery("");
                search.current?.focus();
              }}
            >
              <XIcon aria-hidden="true" />
            </Button>
          )}
        </div>
      )}
      {notice}
      {groups.length > 0 && (
        <div
          ref={list}
          className="min-h-0 flex-1 [scrollbar-width:thin] [scrollbar-color:var(--gray-5)_transparent] overflow-y-auto overscroll-contain"
        >
          {groups.map((group) => (
            <div key={group.label} className="not-first:mt-3">
              <h3 className="text-caption text-gray-7 sticky top-0 z-1 bg-[var(--history-bg)] px-3 pt-1.5 pb-1 leading-[18px] font-medium">
                {group.label}
              </h3>
              <ul className="grid list-none grid-cols-[minmax(0,1fr)] gap-0.5">
                {group.items.map((conversation) => (
                  <li key={conversation.id}>
                    <Link
                      className="text-label text-muted-foreground hover:bg-gray-2 hover:text-foreground aria-[current=page]:bg-gray-3 aria-[current=page]:text-foreground block scroll-mt-7 truncate rounded-md px-3 py-2 leading-5 no-underline pointer-coarse:py-3"
                      to={`/conversations/${encodeURIComponent(conversation.id)}`}
                      aria-current={
                        current === conversation.id ? "page" : undefined
                      }
                      onClick={onNavigate}
                      onMouseEnter={showTruncated}
                    >
                      {label(conversation.preview)}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </section>
  );
};
