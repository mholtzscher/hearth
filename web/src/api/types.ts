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

export type CommandStatus =
  | "requested"
  | "accepted"
  | "satisfied"
  | "dispatched"
  | "rejected"
  | "adapter_unhealthy"
  | "entity_unavailable"
  | "outcome_timeout"
  | "entity_disabled"
  | "internal_failure"
  | "interrupted";

export interface CommandRecord {
  id: string;
  entity_id: string;
  operation: string;
  parameters: Record<string, unknown>;
  status: CommandStatus;
  requested_at: string;
  deadline_at: string;
  accepted_at?: string;
  completed_at?: string;
  outcome_observation_id?: string;
  failure_code?: string;
}

// Satisfied commands carry fresh observation evidence; dispatched commands
// (stateless actions) are terminally accepted with no observation or value.
// Field names match the Go JSON tags exactly: dispatched bodies omit both
// evidence fields instead of rendering null.
export type CommandResult =
  | { command_id: string; status: "satisfied"; observation_id: string; value: unknown }
  | { command_id: string; status: "dispatched" };

export interface Collection<T> {
  items: T[];
  next_cursor?: string;
}

// Typed view of the hearthd v1 Automation API (see
// internal/modules/automations/api/models.go). Field names match the Go JSON
// tags exactly, including the `omitempty` fields that are absent rather than
// null when unset.

/** One typed Observation comparison inside an Observation Trigger. */
export interface AutomationComparison {
  value_pointer: string;
  operator: "eq" | "ne" | "lt" | "lte" | "gt" | "gte";
  operand: unknown;
}

/** One Trigger: an immediate matcher or a held State predicate. */
export interface AutomationTrigger {
  id: string;
  kind: "observation" | "entity_event" | "held_state";
  entity_id: string;
  dispositions?: ("applied" | "unchanged")[];
  comparisons?: AutomationComparison[];
  event_name?: string;
  for_seconds?: number;
}

/** One ordered Step: an Entity Operation request with static parameters. */
export interface AutomationStep {
  id: string;
  entity_id: string;
  operation: string;
  parameters: unknown;
}

/** The strict definition: full replacement replaces all four fields. */
export interface AutomationDefinition {
  name: string;
  enabled: boolean;
  triggers: AutomationTrigger[];
  steps: AutomationStep[];
}

/** One current definition with the revision and timestamps it was written at. */
export interface Automation {
  id: string;
  revision: number;
  created_at: string;
  updated_at: string;
  definition: AutomationDefinition;
}

/** Immutable Device Fact evidence retained in one Run or Skip. */
export interface DeviceFactSummary {
  fact_id: string;
  family: "observation" | "entity_event";
  entity_id: string;
  variant: string;
  causation_id: string;
  observation_value?: unknown;
  emitted_at: string;
}

export type AutomationRunStatus = "running" | "succeeded" | "failed" | "interrupted";

export type AutomationRunSource = "device_fact" | "manual" | "held_state";

export interface HeldStateEvidence {
  trigger_id: string;
  started_at: string;
  due_at: string;
}

export type AutomationStepStatus =
  | "not_attempted"
  | "running"
  | "satisfied"
  | "dispatched"
  | "failed"
  | "interrupted";

export type AutomationSkipReason = "automation_busy" | "stale_fact";

/** One Step attempt. Only ownership-verified Command evidence is exposed, and
    reserved identities never appear. `position` is zero-based and immutable. */
export interface AutomationStepAttempt {
  position: number;
  step_id: string;
  status: AutomationStepStatus;
  verified_command_id?: string;
  failure_code?: string;
  started_at?: string;
  completed_at?: string;
}

/** One recorded Execution: an immutable definition snapshot plus its current
    Step attempts. A manual Run carries no Fact and no matched Trigger IDs. */
export interface AutomationRun {
  id: string;
  automation_id: string;
  automation_name: string;
  revision: number;
  source: AutomationRunSource;
  fact?: DeviceFactSummary;
  held_state?: HeldStateEvidence;
  matched_trigger_ids: string[];
  status: AutomationRunStatus;
  failure_code?: string;
  started_at: string;
  completed_at?: string;
  snapshot: AutomationDefinition;
  steps: AutomationStepAttempt[];
}

/** One recorded Skip: a matched Fact that started no Run, with the Triggers
    that matched. A Skip never queues execution. */
export interface AutomationSkip {
  id: string;
  automation_id: string;
  automation_name: string;
  revision: number;
  fact?: DeviceFactSummary;
  held_state?: HeldStateEvidence;
  matched_triggers: AutomationTrigger[];
  reason: AutomationSkipReason;
  skipped_at: string;
}

/** One newest-first history row: a Run with its status, or a Skip with its reason. */
export interface AutomationHistorySummary {
  id: string;
  kind: "run" | "skip";
  automation_id: string;
  automation_name: string;
  revision: number;
  recorded_at: string;
  status?: AutomationRunStatus;
  reason?: AutomationSkipReason;
  fact?: DeviceFactSummary;
  held_state?: HeldStateEvidence;
}

/** Exactly one retained Run snapshot or Skip detail; the other side is absent. */
export interface AutomationHistoryEntry {
  kind: "run" | "skip";
  run?: AutomationRun;
  skip?: AutomationSkip;
}

// One retained Entity Event report, newest-first by receive order. Accepted
// reports were recorded; rejected reports carry the rejection_code Core gave.
// An Entity Event has no State value, Command link, or payload to show.
// Field names match the Go JSON tags exactly.
export interface EntityEventEntry {
  event_id: string;
  entity_id: string;
  name: string;
  disposition: "accepted" | "rejected" | string;
  rejection_code?: string;
  emitted_at: string;
  received_at: string;
  recorded_at: string;
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
