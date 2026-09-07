import { Highlight, themes } from "prism-react-renderer";
import type { ReactNode } from "react";
import { ApiError } from "../api/client.ts";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "./ui/accordion.tsx";
import { Badge } from "./ui/badge.tsx";
import { Separator } from "./ui/separator.tsx";
import { Table, TableBody, TableCell, TableRow } from "./ui/table.tsx";

/** Link affordance. --primary is near-monochrome in this theme, so links need
    their own color to be distinguishable from body text. */
export const linkClass =
  "text-link underline-offset-4 hover:underline focus-visible:underline focus-visible:outline-none";

/** Problem details carry the useful text in title/detail; showing the raw JSON
    buries it. ApiError.message already resolves detail ?? title ?? status. */
export function ErrorBox({ error }: { error: Error }) {
  const problem =
    error instanceof ApiError && typeof error.detail === "object" ? error.detail : null;
  const heading =
    error instanceof ApiError
      ? [error.status, problem?.title].filter(Boolean).join(" ")
      : error.name || "Error";
  const message = error.message || String(error);
  return (
    <div
      role="alert"
      className="my-2 flex flex-wrap items-baseline gap-x-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
    >
      <span className="font-medium">{heading}</span>
      {message !== problem?.title && <span>{message}</span>}
    </div>
  );
}

type Tone = "default" | "success" | "warning" | "error" | "info";

export function statusTone(status: string): Tone {
  switch (status) {
    case "healthy":
    case "available":
    case "satisfied":
    case "ready":
    case "ok":
      return "success";
    case "dispatched":
      return "info";
    case "unhealthy":
    case "unavailable":
    case "error":
      return "error";
    case "unknown":
    case "disabled":
      return "warning";
    default:
      return "default";
  }
}

/** Healthy states stay quiet; problems carry the color weight. */
const toneClass: Record<Tone, string> = {
  default: "border-border text-muted-foreground",
  success: "border-success/30 bg-success/10 text-success",
  warning: "border-warning/35 bg-warning/10 text-warning",
  error: "border-destructive/40 bg-destructive/15 text-destructive",
  info: "border-link/35 bg-link/10 text-link",
};

export function StatusChip({ label, status }: { label?: string; status: string }) {
  return (
    <Badge variant="outline" className={toneClass[statusTone(status)]}>
      {label ? `${label}: ${status}` : status}
    </Badge>
  );
}

/** Tables render headers even with no rows; say so instead of showing a stub. */
export function EmptyRow({ colSpan, message }: { colSpan: number; message: string }) {
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell colSpan={colSpan} className="py-6 text-center text-muted-foreground">
        {message}
      </TableCell>
    </TableRow>
  );
}

/** Long opaque IDs: monospace, truncated to the column, full value on hover. */
export function MonoId({ value, className = "" }: { value: string; className?: string }) {
  return (
    <span title={value} className={`block truncate font-mono text-xs ${className}`}>
      {value}
    </span>
  );
}

export function RawJson({ value, title = "Raw JSON" }: { value: unknown; title?: string }) {
  return (
    <Accordion type="single" collapsible className="mt-4 rounded-lg border px-3">
      <AccordionItem value="raw-json">
        <AccordionTrigger className="text-sm font-medium">{title}</AccordionTrigger>
        <AccordionContent>
          <JsonCode code={JSON.stringify(value, null, 2) ?? String(value)} />
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  );
}

/** Syntax-highlighted JSON block. Token colors from nightOwl, transparent
    background so the block blends with the surrounding surface. */
const jsonTheme = {
  ...themes.nightOwl,
  plain: { ...themes.nightOwl.plain, backgroundColor: "transparent" },
};

export function JsonCode({ code }: { code: string }) {
  return (
    <Highlight code={code} language="json" theme={jsonTheme}>
      {({ tokens, getLineProps, getTokenProps }) => (
        <pre className="m-0 overflow-auto text-xs">
          {tokens.map((line, i) => (
            <div key={i} {...getLineProps({ line, key: i })}>
              {line.map((token, key) => (
                <span key={key} {...getTokenProps({ token, key })} />
              ))}
            </div>
          ))}
        </pre>
      )}
    </Highlight>
  );
}

export function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mt-6">
      <h2 className="text-sm font-semibold tracking-wide text-muted-foreground uppercase">
        {title}
      </h2>
      <Separator className="mt-1.5 mb-3" />
      {children}
    </section>
  );
}

/** Label/value rows for detail cards. Values render monospace and wrap long IDs. */
export function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <Table className="mt-1">
      <TableBody>
        {rows.map(([label, value]) => (
          <TableRow key={label} className="border-0 hover:bg-transparent">
            <TableCell className="w-36 whitespace-normal px-0 py-0.5 align-top text-muted-foreground">
              {label}
            </TableCell>
            <TableCell className="px-0 py-0.5 align-top font-mono text-xs whitespace-normal wrap-anywhere">
              {value}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
