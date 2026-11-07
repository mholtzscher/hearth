import { automationFetch } from "./automation-json";
import type { Automation } from "./types";
import { serializeAutomationDocument, type AutomationDocument } from "../features/automations/document";
import { ApiError } from "./client";
import { z } from "zod";

const diagnosticSchema = z.object({ code: z.string(), path: z.string(), step_id: z.string().optional(), condition_id: z.string().optional(), span: z.object({ start: z.number().int(), end: z.number().int() }).optional() });
export type AutomationSaveDiagnostic = z.infer<typeof diagnosticSchema> & { message?: string };
export function automationSaveDiagnostics(error: unknown): AutomationSaveDiagnostic[] {
  if (!(error instanceof ApiError)) return [];
  const problem = z.object({ diagnostics: z.array(diagnosticSchema).optional(), errors: z.array(z.object({ location: z.string(), message: z.string() })).optional() }).safeParse(error.detail);
  if (!problem.success) return [];
  const issues = problem.data.diagnostics ?? [];
  const details = problem.data.errors ?? [];
  return issues.length ? issues.map(issue => ({ ...issue, message: details.find(detail => detail.location === `body${issue.path.split("/").slice(1).map(part => /^\d+$/.test(part) ? `[${part}]` : `.${part}`).join("")}`)?.message })) : details.map(detail => ({ code: "invalid_definition", path: detail.location, message: detail.message }));
}

export function getAutomation(id: string, signal?: AbortSignal): Promise<Automation> {
  return automationFetch(`/v1/automations/${encodeURIComponent(id)}`, { signal });
}
export function createAutomation(document: AutomationDocument): Promise<Automation> {
  return automationFetch("/v1/automations", { method: "POST", body: serializeAutomationDocument(document) });
}
export function replaceAutomation(id: string, revision: number, document: AutomationDocument): Promise<Automation> {
  return automationFetch(`/v1/automations/${encodeURIComponent(id)}`, { method: "PUT", body: serializeAutomationDocument(document, revision) });
}
