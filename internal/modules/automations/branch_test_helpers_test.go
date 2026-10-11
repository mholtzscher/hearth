package automations_test

import "github.com/mholtzscher/hearth/internal/modules/automations"

func chooseEvaluations(decision automations.BranchDecision) []automations.ChooseEvaluation {
	body := decision.Body.(automations.ChooseDecision)
	switch result := body.Result.(type) {
	case automations.ChooseSelected:
		return result.Evaluations
	case automations.ChooseFallback:
		return result.Evaluations
	case automations.ChooseUnknown:
		return result.Evaluations
	case automations.ChooseError:
		return result.Evaluations
	default:
		panic("invalid test Choose result")
	}
}

func ifEvaluation(decision automations.BranchDecision) automations.ConditionEvaluation {
	body := decision.Body.(automations.IfDecision)
	switch result := body.Result.(type) {
	case automations.IfSelected:
		return result.Evaluation
	case automations.IfUnknown:
		return result.Evaluation
	case automations.IfError:
		panic("If error has no root")
	default:
		panic("invalid test If result")
	}
}

func mutateChooseSelected(decision *automations.BranchDecision, mutate func(*automations.ChooseSelected)) {
	result := decision.Body.(automations.ChooseDecision).Result.(automations.ChooseSelected)
	mutate(&result)
	decision.Body = automations.ChooseDecision{Result: result}
}

func mutateIfEvaluation(decision *automations.BranchDecision, mutate func(*automations.ConditionEvaluation)) {
	body := decision.Body.(automations.IfDecision)
	switch result := body.Result.(type) {
	case automations.IfSelected:
		mutate(&result.Evaluation)
		body.Result = result
	case automations.IfUnknown:
		mutate(&result.Evaluation)
		body.Result = result
	case automations.IfError:
		panic("If error has no root")
	default:
		panic("invalid test If result")
	}
	decision.Body = body
}

func mutateKnownState(decision *automations.BranchDecision, mutate func(*automations.KnownStateEvidence)) {
	node := &ifEvaluation(*decision).Nodes[0]
	evidence := node.Evidence.(automations.KnownStateEvidence)
	mutate(&evidence)
	node.Evidence = evidence
}
