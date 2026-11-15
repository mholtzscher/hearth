import { ApiError } from "../api/client.ts";
import { Notice } from "./notice.tsx";

/** Problem details carry the useful text in title/detail; showing the raw JSON
    buries it. ApiError.message already resolves detail ?? title ?? status. */
export const ErrorBox = ({ error }: { error: Error }) => {
  const problem =
    error instanceof ApiError && typeof error.detail === "object"
      ? error.detail
      : null;
  const heading =
    error instanceof ApiError
      ? [error.status, problem?.title].filter(Boolean).join(" ")
      : error.name || "Error";
  const message = error.message || String(error);
  return (
    <Notice role="alert" className="flex flex-wrap items-baseline gap-x-2">
      <span className="font-medium">{heading}</span>
      {message !== problem?.title && <span>{message}</span>}
    </Notice>
  );
};
