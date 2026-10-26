//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise app assembly and lifecycle boundaries.

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

type branchingHistoryRun struct {
	conditionsRun

	FailureCode *string              `json:"failure_code"`
	Executions  []branchingExecution `json:"executions"`
}

type branchingExecution struct {
	StepID    string          `json:"step_id"`
	Sequence  int             `json:"sequence"`
	Kind      string          `json:"kind"`
	Phase     string          `json:"phase"`
	StartedAt time.Time       `json:"started_at"`
	Evidence  json.RawMessage `json:"evidence"`
}

type branchingEvidence struct {
	Outcome          string  `json:"outcome"`
	SelectedBranchID *string `json:"selected_branch_id"`
	Evaluations      []struct {
		BranchID   *string `json:"branch_id"`
		Evaluation struct {
			EvaluatedAt time.Time `json:"evaluated_at"`
			Result      string    `json:"result"`
			Nodes       []struct {
				ID                string          `json:"id"`
				SelectedValue     json.RawMessage `json:"selected_value"`
				Kind              string          `json:"kind"`
				MatchedTriggerIDs *[]string       `json:"matched_trigger_ids"`
			} `json:"nodes"`
		} `json:"evaluation"`
	} `json:"evaluations"`
	VerifiedCommandID *string `json:"verified_command_id"`
}

func assertBranchingTriggerEvidence(
	t *testing.T,
	run branchingHistoryRun,
	source string,
	matched, intersection []string,
) {
	t.Helper()
	decisions := branchingDecisions(t, run)
	if run.Cause.Kind != source || !slices.Equal(run.MatchedTriggerIDs, matched) || len(decisions) != 1 {
		t.Fatalf("trigger provenance = %#v", run)
	}
	decision := decisions[0]
	if decision.evidence.Outcome != "then" || len(decision.evidence.Evaluations) != 1 ||
		len(decision.evidence.Evaluations[0].Evaluation.Nodes) != 1 {
		t.Fatalf("trigger decision = %#v", decision)
	}
	node := decision.evidence.Evaluations[0].Evaluation.Nodes[0]
	if node.Kind != "trigger" || node.MatchedTriggerIDs == nil ||
		!slices.Equal(*node.MatchedTriggerIDs, intersection) ||
		node.SelectedValue != nil {
		t.Fatalf("trigger evidence = %#v", node)
	}
}

type branchingDecodedDecision struct {
	execution branchingExecution
	evidence  branchingEvidence
}

func branchingDecisions(t *testing.T, run branchingHistoryRun) []branchingDecodedDecision {
	t.Helper()
	var decisions []branchingDecodedDecision
	for _, execution := range run.Executions {
		if execution.Kind != "if" && execution.Kind != "choose" {
			continue
		}
		var evidence branchingEvidence
		if err := json.Unmarshal(execution.Evidence, &evidence); err != nil {
			t.Fatalf("decode branch execution evidence: %v", err)
		}
		decisions = append(decisions, branchingDecodedDecision{execution: execution, evidence: evidence})
	}
	return decisions
}

func branchingExecutionForStep(
	t *testing.T,
	executions []branchingExecution,
	stepID string,
) (branchingExecution, branchingEvidence, bool) {
	t.Helper()
	for _, execution := range executions {
		if execution.StepID != stepID {
			continue
		}
		var evidence branchingEvidence
		if err := json.Unmarshal(execution.Evidence, &evidence); err != nil {
			t.Fatalf("decode execution %q evidence: %v", stepID, err)
		}
		return execution, evidence, true
	}
	return branchingExecution{}, branchingEvidence{}, false
}

func requireVerifiedCommandLink(t *testing.T, executions []branchingExecution, stepID string) string {
	t.Helper()
	execution, evidence, ok := branchingExecutionForStep(t, executions, stepID)
	if !ok || execution.Kind != "command" || execution.Phase != "succeeded" || evidence.VerifiedCommandID == nil {
		t.Fatalf("command execution %q lacks a verified link: %#v", stepID, execution)
	}
	return *evidence.VerifiedCommandID
}

func requireSingleVerifiedCommandLink(t *testing.T, executions []branchingExecution) string {
	t.Helper()
	var commandID string
	for _, execution := range executions {
		if execution.Kind != "command" {
			continue
		}
		var evidence branchingEvidence
		if err := json.Unmarshal(execution.Evidence, &evidence); err != nil {
			t.Fatalf("decode command execution evidence: %v", err)
		}
		if execution.Phase != "succeeded" || evidence.VerifiedCommandID == nil || commandID != "" {
			t.Fatalf("expected exactly one verified command execution: %#v", executions)
		}
		commandID = *evidence.VerifiedCommandID
	}
	if commandID == "" {
		t.Fatalf("missing verified command execution: %#v", executions)
	}
	return commandID
}
