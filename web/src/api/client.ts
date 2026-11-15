import { useEffect, useState } from "react";

import type { ProblemDetail } from "./types.ts";

export class ApiError extends Error {
  status: number;
  detail: ProblemDetail | string;
  constructor(status: number, detail: ProblemDetail | string) {
    super(
      typeof detail === "string"
        ? detail
        : (detail.detail as string) || detail.title || `HTTP ${status}`
    );
    this.name = "ApiError";
    this.status = status;
    this.detail = detail;
  }
}

/** Base URL for hearthd. Empty string = same origin (vite dev proxy). */
export const getBaseUrl = (): string =>
  localStorage.getItem("hearth.baseUrl") ?? "";

export const setBaseUrl = (value: string): void => {
  const next = value.replace(/\/$/u, "");
  const prev = localStorage.getItem("hearth.baseUrl") ?? "";
  if (prev === next) {
    return;
  }
  localStorage.setItem("hearth.baseUrl", next);
  baseUrlVersion += 1;
  for (const listener of baseUrlListeners) {
    listener();
  }
};

let baseUrlVersion = 0;
const baseUrlListeners = new Set<() => void>();

/** Reactive base-URL generation for request identity (see useApi). */
export const useBaseUrlVersion = (): number => {
  const [version, setVersion] = useState(baseUrlVersion);
  useEffect(() => {
    const listener = () => setVersion(baseUrlVersion);
    baseUrlListeners.add(listener);
    return () => {
      baseUrlListeners.delete(listener);
    };
  }, []);
  return version;
};

const joinUrl = (base: string, path: string): string => {
  if (!base) {
    return path;
  }
  return `${base}${path}`;
};

export const apiFetch = async <T>(
  path: string,
  init?: RequestInit,
  decode: (text: string) => T = JSON.parse
): Promise<T> => {
  // Only send a JSON content type with a body: it makes even GETs preflighted
  // cross-origin, while hearthd serves no CORS headers.
  const hasBody = init?.body !== undefined && init?.body !== null;
  const res = await fetch(joinUrl(getBaseUrl(), path), {
    ...init,
    headers: {
      ...(hasBody ? { "content-type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  const text = await res.text();
  let body: T;
  if (text) {
    body = res.ok ? decode(text) : (JSON.parse(text) as T);
  } else {
    body = undefined as T;
  }
  if (!res.ok) {
    throw new ApiError(res.status, (body as ProblemDetail) ?? res.statusText);
  }
  return body;
};

export const api = {
  health: () => apiFetch<{ status: string }>("/healthz"),
  ready: () => apiFetch<{ status: string }>("/readyz"),
  openapiUrl: () => `${getBaseUrl() || ""}/openapi.json`,
};
