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
  | { id: string; kind: "expression"; expr: string }
  | { id: string; kind: "trigger"; trigger_ids: string[] }
  | { id: string; kind: "all"; children: AutomationBranchCondition[] }
  | { id: string; kind: "any"; children: AutomationBranchCondition[] }
  | { id: string; kind: "not"; child: AutomationBranchCondition };

export type AutomationSetVariablesStep = { id: string; kind: "set_variables"; assignments?: Record<string, unknown>; assignments_expr?: Record<string, string> };
export type AutomationReadStateStep = { id: string; kind: "read_state"; entity_id: string; value_pointer: string; max_age_seconds?: number; on_unusable?: "fail" | "null" };
export type AutomationStep = AutomationCommandStep | AutomationIfStep | AutomationChooseStep | AutomationDelayStep | AutomationNotificationStep | AutomationFunctionStep | AutomationSetVariablesStep | AutomationReadStateStep;

export type AutomationFunctionStep = {
  id: string;
  kind: "function";
  adapter_id: string;
  function_key: string;
  contract_fingerprint: string;
} & ({ arguments: Record<string, unknown>; arguments_expr?: never } | { arguments?: never; arguments_expr: string });

export type AutomationNotificationStep = {
  id: string;
  kind: "notification";
  destination_id: string;
} & ({ title: string; title_expr?: never } | { title?: never; title_expr: string })
  & ({ message: string; message_expr?: never } | { message?: never; message_expr: string });

export interface AutomationDelayStep {
  id: string;
  kind: "delay";
  duration_ms: number;
}

export type AutomationCommandStep = {
  id: string;
  kind: "command";
  entity_id: string;
  operation: string;
} & ({ parameters: Record<string, unknown>; parameters_expr?: never } | { parameters?: never; parameters_expr: string });

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
  | { id: string; kind: "expression"; result: "true" | "false"; value: boolean }
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

type IfEvaluation = [{ evaluation: AutomationConditionEvaluation }];
type ChooseEvaluation = { branch_id: string; evaluation: AutomationConditionEvaluation }[];
type BranchOperationalFailure = "branch_state_read_failed" | "branch_snapshot_incomplete" | "branch_state_corrupt";
type BranchValueFailureCode = "expression_missing" | "expression_type" | "expression_evaluation" | "expression_value_invalid" | "expression_limit" | "resolved_input_invalid" | "run_data_limit";
type BranchError<E> = { [C in BranchValueFailureCode]: { outcome: "error"; evaluations: E; failure_code: C; value_failure: AutomationValueFailure & { failure_code: C; field?: never } & (C extends "run_data_limit" ? { condition_id?: string } : { condition_id: string }) } }[BranchValueFailureCode]
  | { outcome: "error"; evaluations: E; failure_code: BranchOperationalFailure; value_failure?: never };
export type AutomationIfEvidence =
  | { outcome: "then" | "else" | "no_match"; evaluations: IfEvaluation }
  | { outcome: "unknown"; evaluations: IfEvaluation; failure_code: "branch_condition_unknown" }
  | BranchError<[]>;
export type AutomationChooseEvidence =
  | { outcome: "branch"; selected_branch_id: string; evaluations: ChooseEvaluation }
  | { outcome: "default" | "no_match"; evaluations: ChooseEvaluation }
  | { outcome: "unknown"; evaluations: ChooseEvaluation; failure_code: "branch_condition_unknown" }
  | BranchError<ChooseEvaluation>;

/** Full replacement supplies the complete definition, including optional Conditions. */
export interface AutomationDefinition {
  name: string;
  enabled: boolean;
  triggers: AutomationTrigger[];
  conditions?: AutomationCondition;
  variables?: Record<string, unknown>;
  variables_expr?: Record<string, string>;
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

export type AutomationSkipReason =
  | "automation_busy"
  | "stale_fact"
  | "conditions_false"
  | "conditions_unknown";

export type AutomationStateUnusableReason = "entity_missing" | "state_missing" | "pointer_missing" | "evidence_in_future" | "evidence_expired";
type StateReadEvidence = { entity_id: string; value_pointer: string; read_at: string } & (
  | { unusable_reason: "entity_missing" | "state_missing"; observation?: never }
  | { unusable_reason: "pointer_missing" | "evidence_in_future" | "evidence_expired"; observation: ObservationEvidence }
);
type ValueFailureCode = BranchValueFailureCode | "read_state_read_failed" | "read_state_snapshot_incomplete" | "read_state_corrupt";
export type AutomationValueFailure = (
  | ({ field: string; condition_id?: never } | { field?: never; condition_id: string } | { field?: never; condition_id?: never }) & { failure_code: ValueFailureCode; state?: never }
  | { field?: never; condition_id?: never; failure_code: "read_state_unusable"; state: StateReadEvidence }
);
export type AutomationValueInitialization = { kind: "initialized"; recorded_at: string; values: Record<string, unknown> } | { kind: "failed"; recorded_at: string; failure: AutomationValueFailure & { field: string; condition_id?: never } };

/** One durably reached Step, ordered across all kinds. Configuration lives in the
    immutable snapshot; reserved identities alone never prove external effects. */
export type AutomationStepExecution = {
  step_id: string;
  sequence: number;
  started_at: string;
} & (
  | { phase: "running"; completed_at?: never }
  | { phase: "succeeded" | "failed" | "interrupted"; completed_at: string }
) & (
  | ({ kind: "command" } & ({ evidence: {
      command_id?: string; correlation_id?: string; verified_command_id?: string;
      command_outcome?: "satisfied" | "dispatched"; reason_code?: string;
      inputs?: { parameters: Record<string, unknown> }; value_failure?: never;
    } } | { phase: "failed"; evidence: { value_failure: AutomationValueFailure; command_id?: never; correlation_id?: never; verified_command_id?: never; command_outcome?: never; inputs?: never; reason_code?: never } }))
  | { kind: "delay"; evidence: { reason_code?: string } }
  | ({ kind: "notification" } & (
      | { evidence: { attempt_id: string; outcome?: "accepted" | "rejected" | "not_sent" | "unknown"; reason_code?: string; inputs?: { title: string; message: string }; value_failure?: never } }
      | { phase: "failed"; evidence: { attempt_id?: never; outcome?: never; inputs?: never; reason_code?: never; value_failure: AutomationValueFailure } }))
  | ({ kind: "function" } & (
      | { evidence: { invocation_id: string; catalog_revision?: string; inputs?: { arguments: Record<string, unknown> }; invocation_outcome?: "succeeded" | "failed" | "not_executed" | "unknown"; reason_code?: string; output?: never; value_failure?: never } }
      | { phase: "succeeded"; evidence: { invocation_id: string; catalog_revision: string; inputs?: { arguments: Record<string, unknown> }; invocation_outcome: "succeeded"; reason_code?: string; output: unknown; value_failure?: never } }
      | { phase: "failed"; evidence: { invocation_id: string; catalog_revision: string; inputs?: { arguments: Record<string, unknown> }; invocation_outcome: "succeeded"; reason_code?: never; output?: never; value_failure: AutomationValueFailure & { field?: never; condition_id?: never; failure_code: "expression_value_invalid" | "expression_limit" | "run_data_limit" } } }
      | { phase: "failed"; evidence: { invocation_id?: never; catalog_revision?: never; inputs?: never; invocation_outcome?: never; output?: never; reason_code?: never; value_failure: AutomationValueFailure } }))
  | { kind: "if"; evidence: AutomationIfEvidence }
  | { kind: "choose"; evidence: AutomationChooseEvidence }
  | { kind: "read_state"; phase: "succeeded"; evidence: { read_at: string; value_failure?: never } & (
      | { output: unknown; observation: ObservationEvidence; unusable_reason?: never }
      | { output: null; observation?: never; unusable_reason: "entity_missing" | "state_missing" }
      | { output: null; observation: ObservationEvidence; unusable_reason: "pointer_missing" | "evidence_in_future" | "evidence_expired" }
    ) }
  | { kind: "read_state"; phase: "failed"; evidence: { value_failure: AutomationValueFailure; output?: never; read_at?: never; observation?: never; unusable_reason?: never } }
  | { kind: "set_variables"; phase: "succeeded"; evidence: { values: Record<string, unknown>; value_failure?: never } }
  | { kind: "set_variables"; phase: "failed"; evidence: { value_failure: AutomationValueFailure; values?: never } }
);

interface AutomationRunIdentity {
  id: string;
  automation_id: string;
  automation_name: string;
  revision: number;
  cause: AutomationAdmissionCause;
  matched_trigger_ids: string[];
  started_at: string;
  snapshot: AutomationDefinition;
  executions: AutomationStepExecution[];
  data_evidence: "recorded" | "legacy_unavailable";
  value_initialization: AutomationValueInitialization | null;
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
