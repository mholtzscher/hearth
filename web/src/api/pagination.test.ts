import { afterEach, describe, expect, it, vi } from "vitest";
import {
  installCollectionFetch,
  pageCursorForOffset,
  pagedCollection,
} from "./collection-fetch-fake.ts";
import {
  COLLECTION_PAGE_LIMIT,
  CollectionPaginationError,
  MAX_COLLECTION_PAGES,
  fetchAllCollectionPages,
} from "./pagination.ts";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchAllCollectionPages", () => {
  it("reads every page and concatenates them in server order", async () => {
    const { requests } = installCollectionFetch({
      "/v1/entities": pagedCollection(["ent_1", "ent_2", "ent_3", "ent_4", "ent_5"], 2),
    });

    const collection = await fetchAllCollectionPages<string>("/v1/entities");

    expect(collection.items).toEqual(["ent_1", "ent_2", "ent_3", "ent_4", "ent_5"]);
    // Every page was consumed, so there is no cursor left to hand a caller.
    expect(collection.next_cursor).toBeUndefined();
    expect(requests).toEqual([
      "/v1/entities?limit=200",
      `/v1/entities?limit=200&cursor=${encodeURIComponent(pageCursorForOffset(2))}`,
      `/v1/entities?limit=200&cursor=${encodeURIComponent(pageCursorForOffset(4))}`,
    ]);
  });

  it("asks for the largest page hearthd accepts on every page", async () => {
    const { requests } = installCollectionFetch({
      "/v1/entities": pagedCollection(["ent_1", "ent_2", "ent_3", "ent_4"], 1),
    });

    await fetchAllCollectionPages("/v1/entities");

    expect(COLLECTION_PAGE_LIMIT).toBe(200);
    expect(requests).toHaveLength(4);
    for (const url of requests) {
      expect(url).toContain("limit=200");
    }
  });

  it("percent-encodes an opaque cursor so the server reads it back unchanged", async () => {
    const { requests } = installCollectionFetch({
      "/v1/entities": pagedCollection(["ent_1", "ent_2", "ent_3"], 2),
    });

    const collection = await fetchAllCollectionPages<string>("/v1/entities");

    // The fixture cursor only decodes back to what the fake served when the
    // reserved characters are encoded: `+` unencoded would arrive as a space.
    expect(pageCursorForOffset(2)).toBe("page/2+=");
    expect(requests[1]).toBe("/v1/entities?limit=200&cursor=page%2F2%2B%3D");
    expect(collection.items).toEqual(["ent_1", "ent_2", "ent_3"]);
  });

  it("stops when the server omits the next cursor, including an empty one", async () => {
    const omitted = installCollectionFetch({
      "/v1/entities": () => ({ items: ["ent_1"] }),
    });
    expect((await fetchAllCollectionPages("/v1/entities")).items).toEqual(["ent_1"]);
    expect(omitted.requests).toHaveLength(1);

    const empty = installCollectionFetch({
      "/v1/entities": () => ({ items: ["ent_1"], next_cursor: "" }),
    });
    expect((await fetchAllCollectionPages("/v1/entities")).items).toEqual(["ent_1"]);
    expect(empty.requests).toHaveLength(1);
  });

  it("keeps an existing query string when it appends the page parameters", async () => {
    const { requests } = installCollectionFetch({
      "/v1/entities": pagedCollection(["ent_1", "ent_2"], 1),
    });

    await fetchAllCollectionPages("/v1/entities?device_id=dev_kitchen");

    expect(requests).toEqual([
      "/v1/entities?device_id=dev_kitchen&limit=200",
      `/v1/entities?device_id=dev_kitchen&limit=200&cursor=${encodeURIComponent(
        pageCursorForOffset(1),
      )}`,
    ]);
  });

  it("fails instead of looping when the server repeats a cursor it already served", async () => {
    const { requests } = installCollectionFetch({
      "/v1/entities": () => ({ items: ["ent_1"], next_cursor: pageCursorForOffset(50) }),
    });

    const pageRead = fetchAllCollectionPages("/v1/entities");

    await expect(pageRead).rejects.toThrow(CollectionPaginationError);
    await expect(pageRead).rejects.toThrow("refusing to loop");
    // One page plus the single follow-up that revealed the repeat.
    expect(requests).toHaveLength(2);
  });

  it("fails after MAX_COLLECTION_PAGES instead of returning a partial collection", async () => {
    let pagesServed = 0;
    const { requests } = installCollectionFetch({
      // A fresh cursor every time, so only the page bound can stop this read.
      // The responder also fails the test itself if the bound is missing, so a
      // runaway read reports what happened instead of just timing out.
      "/v1/entities": () => {
        pagesServed += 1;
        if (pagesServed > MAX_COLLECTION_PAGES) {
          throw new Error(
            `collection read never stopped: served more than ${MAX_COLLECTION_PAGES} pages`,
          );
        }
        return { items: ["ent_1"], next_cursor: pageCursorForOffset(pagesServed) };
      },
    });

    const pageRead = fetchAllCollectionPages("/v1/entities");

    await expect(pageRead).rejects.toThrow(CollectionPaginationError);
    await expect(pageRead).rejects.toThrow("refusing to continue");
    expect(MAX_COLLECTION_PAGES).toBe(100);
    expect(requests).toHaveLength(100);
  });
});
