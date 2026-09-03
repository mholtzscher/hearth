import { Accordion, AccordionDetails, AccordionSummary, Box, Chip, Divider, Table, TableBody, TableCell, TableRow, Typography } from "@mui/material";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";
import type { ReactNode } from "react";
import { ApiError } from "../api/client.ts";

export function ErrorBox({ error }: { error: Error }) {
  const detail =
    error instanceof ApiError
      ? typeof error.detail === "string"
        ? `${error.status}: ${error.detail}`
        : `${error.status}: ${JSON.stringify(error.detail)}`
      : error.message;
  return (
    <Box role="alert" sx={{ color: "error.main", my: 1 }}>
      Error: {detail}
    </Box>
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
    case "unhealthy":
    case "unavailable":
      return "error";
    case "unknown":
    case "disabled":
      return "warning";
    default:
      return "default";
  }
}

export function StatusChip({ label, status }: { label?: string; status: string }) {
  return <Chip size="small" color={statusTone(status)} label={label ? `${label}: ${status}` : status} />;
}

export function RawJson({ value, title = "Raw JSON" }: { value: unknown; title?: string }) {
  return (
    <Accordion sx={{ mt: 2 }}>
      <AccordionSummary expandIcon={<ExpandMoreIcon />}>
        <Typography variant="subtitle2">{title}</Typography>
      </AccordionSummary>
      <AccordionDetails>
        <Box component="pre" sx={{ overflow: "auto", fontSize: 12, m: 0 }}>
          {JSON.stringify(value, null, 2)}
        </Box>
      </AccordionDetails>
    </Accordion>
  );
}

export function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Box sx={{ mt: 4 }}>
      <Typography variant="h6" gutterBottom>
        {title}
      </Typography>
      <Divider sx={{ mb: 2 }} />
      {children}
    </Box>
  );
}

/** Label/value rows for detail cards. Values render monospace and wrap long IDs. */
export function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <Table size="small" sx={{ mt: 1, "& td": { border: 0, py: 0.5, px: 0, verticalAlign: "top" } }}>
      <TableBody>
        {rows.map(([label, value]) => (
          <TableRow key={label}>
            <TableCell sx={{ width: 140, color: "text.secondary" }}>{label}</TableCell>
            <TableCell sx={{ fontFamily: "monospace", fontSize: 12.5, wordBreak: "break-all" }}>
              {value}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
