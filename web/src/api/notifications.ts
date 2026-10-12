import { apiFetch } from "./client.ts";

export interface NotificationDestination {
  id: string;
  name: string;
  available: boolean;
}

export interface NotificationAttempt {
  id: string;
  destination_id: string;
  destination_name: string;
  title: string;
  message: string;
  status: "running" | "accepted" | "rejected" | "not_sent" | "unknown";
  started_at: string;
  deadline: string;
  completed_at: string | null;
  reason_code?: string;
  receipt?: string;
}

export interface NotificationPage<T> {
  items: T[];
  next_cursor: string | null;
}

export const notificationsApi = {
  destinations: (cursor = "") => apiFetch<NotificationPage<NotificationDestination>>(
    `/v1/notification-destinations?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
  ),
  destination: (id: string) => apiFetch<NotificationDestination>(`/v1/notification-destinations/${encodeURIComponent(id)}`),
  // Intentionally one fetch: a repeat invocation can produce another message.
  send: (id: string, title: string, message: string) => apiFetch<NotificationAttempt>(
    `/v1/notification-destinations/${encodeURIComponent(id)}/notifications`,
    { method: "POST", body: JSON.stringify({ title, message }) },
  ),
  attempts: (destinationId = "", cursor = "") => apiFetch<NotificationPage<NotificationAttempt>>(
    `/v1/notifications?limit=50${destinationId ? `&destination_id=${encodeURIComponent(destinationId)}` : ""}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
  ),
  attempt: (id: string) => apiFetch<NotificationAttempt>(`/v1/notifications/${encodeURIComponent(id)}`),
};
