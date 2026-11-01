import { z } from "zod";
import { apiFetch } from "./client";
import type { ViewDefinition } from "../features/views/catalog";

const dashboardSchema = z.object({ id: z.string(), revision: z.number().int().positive(), definition: z.unknown(), created_at: z.string(), updated_at: z.string() });
const summarySchema = z.object({ id: z.string(), revision: z.number().int().positive(), title: z.string(), created_at: z.string(), updated_at: z.string() });
const pageSchema = z.object({ items: z.array(summarySchema), next_cursor: z.string().nullish() });
const parse = <T>(schema: z.ZodType<T>) => (text: string): T => schema.parse(JSON.parse(text));
export type Dashboard = z.infer<typeof dashboardSchema>;
export type DashboardSummary = z.infer<typeof summarySchema>;
export const getDashboard = (id: string) => apiFetch(`/v1/dashboards/${encodeURIComponent(id)}`, undefined, parse(dashboardSchema));
export const listDashboards = (cursor?: string) => apiFetch(`/v1/dashboards${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`, undefined, parse(pageSchema));
export const createDashboard = (definition: ViewDefinition) => apiFetch("/v1/dashboards", { method: "POST", body: JSON.stringify({ definition }) }, parse(dashboardSchema));
export const updateDashboard = (id: string, revision: number, definition: ViewDefinition) => apiFetch(`/v1/dashboards/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify({ expected_revision: revision, definition }) }, parse(dashboardSchema));
export const deleteDashboard = (id: string, revision: number) => apiFetch<void>(`/v1/dashboards/${encodeURIComponent(id)}?expected_revision=${revision}`, { method: "DELETE" });
