package sqlite

import (
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// Schedule history has no device evidence and never bypasses Conditions.
func validateScheduleSummary(row dbsqlc.AutomationHistory, summary automations.HistorySummary) error {
	if automations.CauseSource(summary.Cause) != automations.RunSourceSchedule {
		return nil
	}
	if row.FactPreviousValueJson.Valid {
		return fmt.Errorf("%w: schedule history carries device evidence", automations.ErrInvalidAutomation)
	}
	var reason automations.SkipReason
	switch body := summary.Body.(type) {
	case automations.RunHistorySummary:
	case automations.SkipHistorySummary:
		reason = body.Reason
	default:
		return fmt.Errorf("%w: invalid history summary body", automations.ErrInvalidAutomation)
	}
	mode, bypass, result := automations.ConditionSummaryLabels(summary.ConditionSummary)
	return validateScheduleDecision(mode, bypass, result, reason)
}

func validateScheduleRun(run automations.Run) error {
	if automations.CauseSource(run.Cause) != automations.RunSourceSchedule {
		return nil
	}
	triggers, err := automations.MatchedTriggerSnapshots(run.Snapshot, run.MatchedTriggerIDs)
	if err != nil {
		return err
	}
	if err = validateScheduledTriggers(triggers); err != nil {
		return err
	}
	return validateScheduleConditionDecision(run.ConditionDecision, "")
}

func validateScheduleSkip(skip automations.Skip) error {
	if automations.CauseSource(skip.Cause) != automations.RunSourceSchedule {
		return nil
	}
	if err := validateScheduledTriggers(skip.MatchedTriggers); err != nil {
		return err
	}
	return validateScheduleConditionDecision(skip.ConditionDecision, skip.Reason)
}

func validateScheduledTriggers(triggers []automations.Trigger) error {
	if len(triggers) < 1 || len(triggers) > 32 {
		return fmt.Errorf("%w: schedule history requires 1–32 Cron Triggers", automations.ErrInvalidAutomation)
	}
	seen := make(map[automations.TriggerID]bool, len(triggers))
	for _, trigger := range triggers {
		if trigger.Kind() != automations.TriggerKindCron || seen[trigger.ID] {
			return fmt.Errorf("%w: schedule history requires unique Cron Triggers", automations.ErrInvalidAutomation)
		}
		seen[trigger.ID] = true
	}
	return nil
}

func validateScheduleConditionDecision(decision automations.ConditionDecision, reason automations.SkipReason) error {
	if decision == nil {
		return fmt.Errorf("%w: schedule history requires a Condition decision", automations.ErrInvalidAutomation)
	}
	var result *automations.ConditionResult
	if evaluation := decision.DecisionEvaluation(); evaluation != nil {
		result = &evaluation.Result
	}
	return validateScheduleDecision(decision.DecisionMode(), decision.BypassRequested(), result, reason)
}

func validateScheduleDecision(
	mode automations.ConditionDecisionMode,
	bypass bool,
	result *automations.ConditionResult,
	reason automations.SkipReason,
) error {
	valid := false
	if !bypass {
		switch reason {
		case "":
			valid = mode == automations.ConditionDecisionNotConfigured && result == nil ||
				mode == automations.ConditionDecisionEvaluated && result != nil && *result == automations.ConditionTrue
		case automations.SkipBusy:
			valid = (mode == automations.ConditionDecisionNotConfigured || mode == automations.ConditionDecisionNotEvaluated) &&
				result == nil
		case automations.SkipConditionsFalse:
			valid = mode == automations.ConditionDecisionEvaluated && result != nil &&
				*result == automations.ConditionFalse
		case automations.SkipConditionsUnknown:
			valid = mode == automations.ConditionDecisionEvaluated && result != nil &&
				*result == automations.ConditionUnknown
		case automations.SkipStaleFact:
			// A schedule has no Fact whose age could justify this reason.
			valid = false
		}
	}
	if !valid {
		return fmt.Errorf(
			"%w: schedule history has inconsistent Condition decision or reason",
			automations.ErrInvalidAutomation,
		)
	}
	return nil
}
