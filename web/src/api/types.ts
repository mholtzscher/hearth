// Typed view of the hearthd v1 HTTP API (see internal/modules/devices/api).
// Field names match the Go JSON tags exactly.

export interface HealthReason {
  code: string;
}

export interface Availability {
  status: "unknown" | "available" | "unavailable" | string;
  source: string;
  since: string;
  evidence_at: string;
  source_observed_at?: string;
  reason?: HealthReason;
}

export interface AdapterRuntime {
  id: string;
  status: string;
  software_name: string;
  software_version: string;
  claimed_at: string;
  last_heartbeat_at?: string;
  lease_expires_at: string;
}

export interface AdapterHealth extends Availability {
  runtime?: AdapterRuntime;
}

export interface Adapter {
  id: string;
  health: AdapterHealth;
}

export interface EntityState {
  value: unknown;
  observation_id: string;
  adapter_received_at: string;
  source_updated_at?: string;
  observed_at: string;
}

export interface Entity {
  id: string;
  device_id: string;
  adapter_id: string;
  name: string;
  type: string;
  support: Record<string, unknown>;
  enabled: boolean;
  availability: Availability;
  state: EntityState | null;
}

export interface Device {
  id: string;
  kind: string;
  name: string;
}

export interface DeviceDetail extends Device {
  entities: Entity[];
  next_entity_cursor?: string;
}

export interface HealthTransition {
  status: string;
  source: string;
  reason?: HealthReason;
  source_observed_at?: string;
  observed_at: string;
}

export interface CommandRecord {
  id: string;
  entity_id: string;
  operation: string;
  parameters: Record<string, unknown>;
  status: string;
  requested_at: string;
  deadline_at: string;
  accepted_at?: string;
  completed_at?: string;
  outcome_observation_id?: string;
  failure_code?: string;
}

export interface CommandResult {
  command_id: string;
  status: string;
  observation_id: string;
  value: unknown;
}

export interface Collection<T> {
  items: T[];
  next_cursor?: string;
}

export interface EntityStateHistoryEntry {
  observation_id: string;
  value?: unknown;
  disposition: "applied" | "unchanged" | "rejected" | string;
  rejection_code?: string;
  adapter_received_at: string;
  source_updated_at?: string;
  observed_at: string;
}

/** RFC 9457 problem detail returned by hearthd on errors. */
export interface ProblemDetail {
  title?: string;
  status?: number;
  detail?: string;
  [key: string]: unknown;
}
