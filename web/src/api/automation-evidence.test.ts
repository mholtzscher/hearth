import { expect, it } from "vitest";
import type { AutomationStepExecution } from "./types.ts";

const terminal = { sequence: 0, step_id: "s", started_at: "now", completed_at: "now" };
const failure = { path: "steps.s", failure_code: "expression_failed" };

it("models recorded data and legacy effect evidence as distinct variants", () => {
  const legacy: AutomationStepExecution = { ...terminal, kind: "function", phase: "succeeded", evidence: { invocation_id: "inv", invocation_outcome: "succeeded", catalog_revision: "rev" } };
  const success: AutomationStepExecution = { ...terminal, kind: "read_state", phase: "succeeded", evidence: { output: null, read_at: "now", unusable_reason: "state_missing" } };
  const failed: AutomationStepExecution = { ...terminal, kind: "set_variables", phase: "failed", evidence: { value_failure: failure } };
  const preReservation: AutomationStepExecution = { ...terminal, kind: "function", phase: "failed", evidence: { value_failure: failure } };
  const postSuccess: AutomationStepExecution = { ...terminal, kind: "function", phase: "failed", evidence: { invocation_id: "inv", catalog_revision: "rev", invocation_outcome: "succeeded", value_failure: failure } };
  expect([legacy.kind, success.kind, failed.kind, preReservation.kind, postSuccess.kind]).toEqual(["function", "read_state", "set_variables", "function", "function"]);

  // Compile-time negative controls: these cannot describe valid retained evidence.
  // @ts-expect-error data success requires its complete values
  const missing: AutomationStepExecution = { ...terminal, kind: "set_variables", phase: "succeeded", evidence: {} };
  // @ts-expect-error success and failure evidence cannot coexist
  const both: AutomationStepExecution = { ...terminal, kind: "set_variables", phase: "succeeded", evidence: { values: {}, value_failure: failure } };
  // @ts-expect-error state output and failure cannot coexist
  const stateBoth: AutomationStepExecution = { ...terminal, kind: "read_state", phase: "failed", evidence: { output: null, read_at: "now", value_failure: failure } };
  // @ts-expect-error State reasons are the five supported values
  const reason: AutomationStepExecution = { ...terminal, kind: "read_state", phase: "succeeded", evidence: { output: null, read_at: "now", unusable_reason: "other" } };
  // @ts-expect-error Function output and value failure cannot coexist
  const outputAndFailure: AutomationStepExecution = { ...terminal, kind: "function", phase: "failed", evidence: { invocation_id: "inv", catalog_revision: "rev", invocation_outcome: "succeeded", output: null, value_failure: failure } };
  // @ts-expect-error a missing reservation cannot claim a provider outcome
  const fakeOutcome: AutomationStepExecution = { ...terminal, kind: "notification", phase: "failed", evidence: { outcome: "accepted", value_failure: failure } };
  // @ts-expect-error a value failure cannot be a successful Step
  const successfulFailure: AutomationStepExecution = { ...terminal, kind: "function", phase: "succeeded", evidence: { value_failure: failure } };
  void [missing, both, stateBoth, reason, outputAndFailure, fakeOutcome, successfulFailure];
});
