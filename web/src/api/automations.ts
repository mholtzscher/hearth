import { automationFetch } from "./automation-json";
import type { Automation } from "./types";
import { serializeAutomationDocument, type AutomationDocument } from "../features/automations/document";

export function getAutomation(id: string, signal?: AbortSignal): Promise<Automation> {
  return automationFetch(`/v1/automations/${encodeURIComponent(id)}`, { signal });
}
export function createAutomation(document: AutomationDocument): Promise<Automation> {
  return automationFetch("/v1/automations", { method: "POST", body: serializeAutomationDocument(document) });
}
export function replaceAutomation(id: string, revision: number, document: AutomationDocument): Promise<Automation> {
  return automationFetch(`/v1/automations/${encodeURIComponent(id)}`, { method: "PUT", body: serializeAutomationDocument(document, revision) });
}
