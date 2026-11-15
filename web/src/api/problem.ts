import { ApiError } from "./client.ts";

/** Machine-readable `code` from a hearthd RFC 9457 problem response, or null
    when the failure was not a problem detail carrying one. Callers branch on
    this instead of matching error message text. */
export function problemCode(error: unknown): string | null {
  if (!(error instanceof ApiError) || typeof error.detail !== "object") return null;
  const code = (error.detail as { code?: unknown }).code;
  return typeof code === "string" ? code : null;
}
