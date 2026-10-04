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
  adapter_name: string;
  name_override: string | null;
  support: Record<string, unknown>;
  enabled: boolean;
  availability: Availability;
  state: EntityState | null;
}

export interface Device {
  id: string;
  kind: string;
  name: string;
  adapter_name: string;
  name_override: string | null;
}

export interface DevicePatch {
  name_override?: string | null;
}

export interface EntityPatch {
  enabled?: boolean;
  name_override?: string | null;
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

/** Observation matching may compare both the current and previous values. */
export interface AutomationObservationTrigger {
  id: string;
  kind: "observation";
  entity_id: string;
  dispositions: ("applied" | "unchanged")[];
  comparisons?: AutomationComparison[];
  previous_comparisons?: AutomationComparison[];
}

export interface AutomationEntityEventTrigger {
  id: string;
  kind: "entity_event";
  entity_id: string;
  event_name: string;
}

export interface AutomationHeldStateTrigger {
  id: string;
  kind: "held_state";
  entity_id: string;
  comparisons: AutomationComparison[];
  for_seconds: number;
}

export interface AutomationCronTrigger {
  id: string;
  kind: "cron";
  expression: string;
}

export type AutomationTrigger = AutomationObservationTrigger | AutomationEntityEventTrigger | AutomationHeldStateTrigger | AutomationCronTrigger;

export interface AutomationStateCondition extends AutomationComparison {
  id: string;
  kind: "entity_state";
  entity_id: string;
  max_age_seconds?: number;
}

/** Admission Conditions cannot match Trigger IDs, including inside groups. */
export type AutomationCondition = AutomationStateCondition
  | { id: string; kind: "all"; children: AutomationCondition[] }
  | { id: string; kind: "any"; children: AutomationCondition[] }
  | { id: string; kind: "not"; child: AutomationCondition };

export type AutomationBranchCondition = AutomationStateCondition
  | { id: string; kind: "trigger"; trigger_ids: string[] }
  | { id: string; kind: "all"; children: AutomationBranchCondition[] }
  | { id: string; kind: "any"; children: AutomationBranchCondition[] }
  | { id: string; kind: "not"; child: AutomationBranchCondition };

export type AutomationStep = AutomationCommandStep | AutomationIfStep | AutomationChooseStep;

export interface AutomationCommandStep {
  id: string;
  kind: "command";
  entity_id: string;
  operation: string;
  parameters: Record<string, unknown>;
}

export interface AutomationIfStep {
  id: string;
  kind: "if";
  conditions: AutomationBranchCondition;
  then: AutomationStep[];
  else?: AutomationStep[];
}

export interface AutomationChooseStep {
  id: string;
  kind: "choose";
  branches: { id: string; conditions: AutomationBranchCondition; steps: AutomationStep[] }[];
  default?: AutomationStep[];
}

export type AutomationConditionResult = "true" | "false" | "unknown";
type ObservationEvidence = { observation_id: string; observed_at: string };
export type AutomationConditionNodeResult =
  | ({ id: string; kind: "entity_state"; result: "true" | "false"; selected_value: unknown } & ObservationEvidence)
  | { id: string; kind: "entity_state"; result: "unknown"; unknown_reason: "entity_missing" | "state_missing" }
  | ({ id: string; kind: "entity_state"; result: "unknown"; unknown_reason: "pointer_missing" } & ObservationEvidence)
  | ({ id: string; kind: "entity_state"; result: "unknown"; unknown_reason: "type_mismatch"; selected_value: unknown } & ObservationEvidence)
  | ({ id: string; kind: "entity_state"; result: "unknown"; unknown_reason: "evidence_in_future" | "evidence_expired"; selected_value?: unknown } & ObservationEvidence)
  | { id: string; kind: "trigger"; result: "true" | "false"; matched_trigger_ids: string[] };

export interface AutomationConditionEvaluation {
  evaluated_at: string;
  result: "true" | "false" | "unknown";
  nodes: AutomationConditionNodeResult[];
}

export type AutomationConditionDecision =
  | { mode: "not_configured"; bypass_requested: false }
  | { mode: "not_evaluated"; bypass_requested: false; snapshot: AutomationCondition }
  | { mode: "bypassed"; bypass_requested: true; snapshot: AutomationCondition }
  | { mode: "evaluated"; bypass_requested: false; snapshot: AutomationCondition; evaluation: AutomationConditionEvaluation };

interface AutomationBranchDecisionIdentity {
  position: number;
  step_id: string;
  evaluated_at: string;
}
type IfEvaluation = [{ evaluation: AutomationConditionEvaluation }];
type ChooseEvaluation = { branch_id: string; evaluation: AutomationConditionEvaluation }[];
export type AutomationBranchDecision = AutomationBranchDecisionIdentity & (
  | { kind: "if"; outcome: "then" | "else" | "no_match"; evaluations: IfEvaluation }
  | { kind: "if"; outcome: "unknown"; evaluations: IfEvaluation; failure_code: "branch_condition_unknown" }
  | { kind: "if"; outcome: "error"; evaluations: []; failure_code: string }
  | { kind: "choose"; outcome: "branch"; selected_branch_id: string; evaluations: ChooseEvaluation }
  | { kind: "choose"; outcome: "default" | "no_match"; evaluations: ChooseEvaluation }
  | { kind: "choose"; outcome: "unknown"; evaluations: ChooseEvaluation; failure_code: "branch_condition_unknown" }
  | { kind: "choose"; outcome: "error"; evaluations: ChooseEvaluation; failure_code: string }
);

/** Full replacement supplies the complete definition, including optional Conditions. */
export interface AutomationDefinition {
  name: string;
  enabled: boolean;
  triggers: AutomationTrigger[];
  conditions?: AutomationCondition;
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
interface AutomationFactIdentity {
  fact_id: string;
  entity_id: string;
  emitted_at: string;
}
export type AutomationDeviceFact = AutomationFactIdentity & (
  | { family: "observation"; observation_id: string; disposition: "applied" | "unchanged"; value: unknown; previous_value?: unknown }
  | { family: "entity_event"; event_id: string; name: string }
);

export type AutomationRunStatus = "running" | "succeeded" | "failed" | "interrupted";

export type AutomationAdmissionCause =
  | { kind: "manual" }
  | { kind: "device_fact"; fact: AutomationDeviceFact }
  | { kind: "held_state"; evidence: HeldStateEvidence }
  | { kind: "schedule" };

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

export type AutomationSkipReason =
  | "automation_busy"
  | "stale_fact"
  | "conditions_false"
  | "conditions_unknown";

/** One Command Step attempt. Only ownership-verified Command evidence is exposed, and
    reserved identities never appear. `position` is zero-based and immutable. */
interface AutomationStepAttemptIdentity {
  position: number;
  step_id: string;
}
export type AutomationStepAttempt = AutomationStepAttemptIdentity & (
  | { status: "not_attempted" }
  | { status: "running"; started_at: string }
  | { status: "satisfied" | "dispatched"; started_at: string; completed_at: string; verified_command_id: string }
  | { status: "failed" | "interrupted"; started_at: string; completed_at: string; failure_code: string; verified_command_id?: string }
);

/** One recorded Execution: an immutable definition snapshot plus its current
    Command Step attempts. A manual Run carries no Fact and no matched Trigger IDs. */
interface AutomationRunIdentity {
  id: string;
  automation_id: string;
  automation_name: string;
  revision: number;
  cause: AutomationAdmissionCause;
  matched_trigger_ids: string[];
  started_at: string;
  snapshot: AutomationDefinition;
  steps: AutomationStepAttempt[];
  branch_decisions: AutomationBranchDecision[];
  condition_decision: AutomationConditionDecision;
}
export type AutomationRun = AutomationRunIdentity & (
  | { status: "running" }
  | { status: "succeeded"; completed_at: string }
  | { status: "failed" | "interrupted"; completed_at: string; failure_code: string }
);

/** One recorded Skip: a matched Fact that started no Run, with the Triggers
    that matched. A Skip never queues execution. */
export interface AutomationSkip {
  id: string;
  automation_id: string;
  automation_name: string;
  revision: number;
  cause: AutomationAdmissionCause;
  condition_decision: AutomationConditionDecision;
  matched_triggers: AutomationTrigger[];
  reason: AutomationSkipReason;
  skipped_at: string;
}

/** One newest-first history row: a Run with its status, or a Skip with its reason. */
interface AutomationHistorySummaryIdentity {
  id: string;
  automation_id: string;
  automation_name: string;
  revision: number;
  cause: AutomationAdmissionCause;
  recorded_at: string;
}
export type AutomationConditionSummary =
  | { condition_mode: "not_configured"; bypass_requested: false }
  | { condition_mode: "not_evaluated"; bypass_requested: false }
  | { condition_mode: "bypassed"; bypass_requested: true }
  | { condition_mode: "evaluated"; bypass_requested: false; condition_result: AutomationConditionResult };
export type AutomationHistorySummary = AutomationHistorySummaryIdentity & AutomationConditionSummary & (
  | { kind: "run"; status: AutomationRunStatus }
  | { kind: "skip"; reason: AutomationSkipReason }
);

/** Exactly one retained Run snapshot or Skip detail; the other side is absent. */
export type AutomationHistoryEntry =
  | { kind: "run"; run: AutomationRun }
  | { kind: "skip"; skip: AutomationSkip };

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
