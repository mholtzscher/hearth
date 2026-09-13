import { vi } from "vitest";

/**
 * Test-only fake `fetch` for hearthd collection reads.
 *
 * Every dashboard read goes through `apiFetch`, which calls the global `fetch`,
 * so stubbing that global is the seam that lets a test serve
 * `{ items, next_cursor }` pages without a server. One definition site, shared
 * by the pagination unit tests and the Device Facts page tests, so the fake page
 * protocol is specified once.
 *
 * The cursors handed out here deliberately carry the reserved characters an
 * opaque cursor may hold (`/`, `+`, `=`). A reader that forgets to
 * percent-encode a cursor sends `+` back as a space, the responder no longer
 * recognizes its own cursor, and the test fails instead of quietly passing a
 * mangled cursor through.
 */

/** One hearthd collection page: the items, plus the cursor for the next page. */
export interface CollectionPage<T> {
  items: T[];
  next_cursor?: string;
}

/** One collection read the fake served, with its cursor decoded. */
export interface CollectionRequest {
  /** Decoded `cursor` value, or null on a first page. */
  cursor: string | null;
}

/** Handle on one installed fake: the request URLs it served, in order. */
export interface CollectionFetchFake {
  requests: string[];
}

const FAKE_ORIGIN = "http://hearthd.test";
const PAGE_CURSOR_PATTERN = /^page\/(\d+)\+=$/;

/** Opaque cursor for the page starting at `nextOffset`. Holds `/`, `+` and `=`
    so following it without URL-encoding cannot round-trip. */
export function pageCursorForOffset(nextOffset: number): string {
  return `page/${nextOffset}+=`;
}

/** Install a fake `fetch` answering one collection responder per path, and
    record the request URLs. A read of a path with no responder fails the test. */
export function installCollectionFetch(
  responders: Record<string, (request: CollectionRequest) => CollectionPage<unknown>>,
): CollectionFetchFake {
  const requests: string[] = [];
  vi.stubGlobal("fetch", async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input);
    requests.push(url);
    // Yield to the macrotask queue like a real round trip does. An immediate
    // responder would let a reader that never stops starve the runner's
    // timers, wedging the whole run instead of failing the test.
    await new Promise((resolve) => setTimeout(resolve, 0));
    const parsed = new URL(url, FAKE_ORIGIN);
    const responder = responders[parsed.pathname];
    if (!responder) {
      throw new Error(`collection fetch fake has no responder for ${parsed.pathname}`);
    }
    const page = responder({ cursor: parsed.searchParams.get("cursor") });
    return new Response(JSON.stringify(page), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  });
  return { requests };
}

/** Serve fixed-size pages of `items`, keyed by the offset in the offered cursor.
    Ignores the requested page size, the way a server with a smaller page cap does. */
export function pagedCollection<T>(
  items: T[],
  pageSize: number,
): (request: CollectionRequest) => CollectionPage<T> {
  return ({ cursor }) => {
    const offset = cursor === null ? 0 : pageOffsetFromCursor(cursor);
    const page = items.slice(offset, offset + pageSize);
    const nextOffset = offset + page.length;
    return nextOffset < items.length
      ? { items: page, next_cursor: pageCursorForOffset(nextOffset) }
      : { items: page };
  };
}

function pageOffsetFromCursor(cursor: string): number {
  const match = PAGE_CURSOR_PATTERN.exec(cursor);
  if (!match) {
    throw new Error(`collection fetch fake got an unrecognized cursor: ${cursor}`);
  }
  return Number(match[1]);
}
