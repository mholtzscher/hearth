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

	FailureCode     *string `json:"failure_code"`
	BranchDecisions []struct {
		Position         int       `json:"position"`
		StepID           string    `json:"step_id"`
		Outcome          string    `json:"outcome"`
		SelectedBranchID *string   `json:"selected_branch_id"`
		EvaluatedAt      time.Time `json:"evaluated_at"`
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
	} `json:"branch_decisions"`
}

func assertBranchingTriggerEvidence(
	t *testing.T,
	run branchingHistoryRun,
	source string,
	matched, intersection []string,
) {
	t.Helper()
	if run.Cause.Kind != source || !slices.Equal(run.MatchedTriggerIDs, matched) || len(run.BranchDecisions) != 1 {
		t.Fatalf("trigger provenance = %#v", run)
	}
	decision := run.BranchDecisions[0]
	if decision.Outcome != "then" || len(decision.Evaluations) != 1 ||
		len(decision.Evaluations[0].Evaluation.Nodes) != 1 {
		t.Fatalf("trigger decision = %#v", decision)
	}
	node := decision.Evaluations[0].Evaluation.Nodes[0]
	if node.Kind != "trigger" || node.MatchedTriggerIDs == nil ||
		!slices.Equal(*node.MatchedTriggerIDs, intersection) ||
		node.SelectedValue != nil {
		t.Fatalf("trigger evidence = %#v", node)
	}
}
