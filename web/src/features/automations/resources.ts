import { apiFetch } from "../../api/client";
import type { Adapter } from "../../api/types";

export interface AdapterFunction {
  adapter_id: string;
  function_key: string;
  contract_fingerprint: string;
  description: string;
  input_schema: Record<string, unknown>;
  callable: boolean;
}

/** Complete collection reads; never present an incomplete page as the catalog. */
export const collection = <T>(path: string): Promise<T[]> => {
  const items: T[] = [];
  const seen = new Set<string>();
  // Paging is sequential by contract: each page's cursor comes from the last
  // response. Recursion keeps the bounded, await-per-page read without a loop.
  const readPage = async (cursor: string, page: number): Promise<T[]> => {
    const result = await apiFetch<{ items: T[]; next_cursor?: string | null }>(
      `${path}?limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
    );
    items.push(...result.items);
    if (!result.next_cursor) {
      return items;
    }
    if (seen.has(result.next_cursor)) {
      throw new Error(`Resource pagination repeated a cursor for ${path}.`);
    }
    seen.add(result.next_cursor);
    if (page + 1 >= 100) {
      throw new Error(`Resource pagination exceeded its limit for ${path}.`);
    }
    return readPage(result.next_cursor, page + 1);
  };
  return readPage("", 0);
};

export const discoverFunctions = async (): Promise<{
  functions: AdapterFunction[];
  errors: string[];
}> => {
  const adapters = await collection<Adapter>("/v1/adapters");
  const pages = await Promise.allSettled(
    adapters.map((adapter) =>
      collection<AdapterFunction>(
        `/v1/adapters/${encodeURIComponent(adapter.id)}/functions`
      )
    )
  );
  return {
    functions: pages.flatMap((page) =>
      page.status === "fulfilled" ? page.value : []
    ),
    errors: pages.flatMap((page, index) =>
      page.status === "rejected"
        ? [`${adapters[index].id}: ${String(page.reason)}`]
        : []
    ),
  };
};
