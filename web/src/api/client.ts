import type { ProblemDetail } from "./types.ts";
import { useEffect, useState } from "react";

export class ApiError extends Error {
  status: number;
  detail: ProblemDetail | string;
  constructor(status: number, detail: ProblemDetail | string) {
    super(typeof detail === "string" ? detail : (detail.detail as string) || detail.title || `HTTP ${status}`);
    this.status = status;
    this.detail = detail;
  }
}

/** Base URL for hearthd. Empty string = same origin (vite dev proxy). */
export function getBaseUrl(): string {
  return localStorage.getItem("hearth.baseUrl") ?? "";
}

export function setBaseUrl(value: string): void {
  const next = value.replace(/\/$/, "");
  const prev = localStorage.getItem("hearth.baseUrl") ?? "";
  if (prev === next) return;
  localStorage.setItem("hearth.baseUrl", next);
  baseUrlVersion++;
  baseUrlListeners.forEach((listener) => listener());
}

let baseUrlVersion = 0;
const baseUrlListeners = new Set<() => void>();

/** Reactive base-URL generation for request identity (see useApi). */
export function useBaseUrlVersion(): number {
  const [version, setVersion] = useState(baseUrlVersion);
  useEffect(() => {
    const listener = () => setVersion(baseUrlVersion);
    baseUrlListeners.add(listener);
    return () => {
      baseUrlListeners.delete(listener);
    };
  }, []);
  return version;
}

function joinUrl(base: string, path: string): string {
  if (!base) return path;
  return `${base}${path}`;
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  // Only send a JSON content type with a body: it makes even GETs preflighted
  // cross-origin, while hearthd serves no CORS headers.
  const hasBody = init?.body !== undefined && init?.body !== null;
  const res = await fetch(joinUrl(getBaseUrl(), path), {
    ...init,
    headers: { ...(hasBody ? { "content-type": "application/json" } : {}), ...(init?.headers ?? {}) },
  });
  const text = await res.text();
  const body = text ? (JSON.parse(text) as T) : (undefined as T);
  if (!res.ok) {
    throw new ApiError(res.status, (body as ProblemDetail) ?? res.statusText);
  }
  return body;
}

export const api = {
  health: () => apiFetch<{ status: string }>("/healthz"),
  ready: () => apiFetch<{ status: string }>("/readyz"),
  openapiUrl: () => `${getBaseUrl() || ""}/openapi.json`,
};
