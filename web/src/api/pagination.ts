import { apiFetch } from "./client.ts";
import type { Collection } from "./types.ts";

/** Largest page hearthd accepts on a collection read (see the `limit` maximum
    on the v1 list endpoints). Requesting the maximum keeps the page count — and
    therefore the number of round trips — as low as the API allows. */
export const COLLECTION_PAGE_LIMIT = 200;

/** Safety bound on automatically followed pages. A completeness read must not
    loop forever if the server ever echoes a cursor it has already served, so
    the helper refuses to request more than this many pages and fails loudly
    instead of silently returning a prefix of the collection. */
export const MAX_COLLECTION_PAGES = 100;

/** Raised when automatic pagination cannot safely finish: the server repeated a
    cursor it had already served, or the collection outran MAX_COLLECTION_PAGES.
    Either case means the items gathered so far would be incomplete, so the read
    fails rather than returning partial data. */
export class CollectionPaginationError extends Error {
  name = "CollectionPaginationError";
}

/** Path for one page of a collection read: always the maximum page size, plus
    the continuation cursor (URL-encoded, since cursors are opaque and may carry
    `+`, `/`, `=` and other reserved characters) when following one. */
function collectionPagePath(path: string, cursor: string | undefined): string {
  const params = `limit=${COLLECTION_PAGE_LIMIT}${
    cursor === undefined ? "" : `&cursor=${encodeURIComponent(cursor)}`
  }`;
  return `${path}${path.includes("?") ? "&" : "?"}${params}`;
}

/** Read every page of a collection endpoint and return them as one Collection.

    Starts with `limit=200`, follows each `next_cursor` until the server omits
    it, and concatenates the pages in order. Cursor values are opaque, so they
    are only ever echoed back URL-encoded; the helper never interprets them.

    Two guards keep the loop safe: a cursor equal to one already followed is
    rejected (the server is not making progress), and a collection needing more
    than MAX_COLLECTION_PAGES pages is rejected. Both throw
    CollectionPaginationError, so a caller's error handling sees a failed read
    instead of a silently truncated one. The returned Collection carries no
    `next_cursor`: every page was consumed. */
export async function fetchAllCollectionPages<T>(path: string): Promise<Collection<T>> {
  const items: T[] = [];
  const followedCursors = new Set<string>();
  let cursor: string | undefined;
  let pagesFetched = 0;

  for (;;) {
    const page = await apiFetch<Collection<T>>(collectionPagePath(path, cursor));
    pagesFetched += 1;
    items.push(...page.items);

    const next = page.next_cursor;
    if (next === undefined || next === "") return { items };
    if (followedCursors.has(next)) {
      throw new CollectionPaginationError(
        `pagination for ${path} repeated cursor ${JSON.stringify(next)} after ${pagesFetched} page(s); refusing to loop`,
      );
    }
    if (pagesFetched >= MAX_COLLECTION_PAGES) {
      throw new CollectionPaginationError(
        `pagination for ${path} exceeded ${MAX_COLLECTION_PAGES} pages at limit=${COLLECTION_PAGE_LIMIT}; refusing to continue`,
      );
    }
    followedCursors.add(next);
    cursor = next;
  }
}
