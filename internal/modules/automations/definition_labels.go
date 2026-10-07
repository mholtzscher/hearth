package automations

// Kind reports the wire label of a supported Step value, or empty for invalid input.
func (step Step) Kind() StepKind {
	switch step.Body.(type) {
	case CommandStep:
		return StepKindCommand
	case IfStep:
		return StepKindIf
	case ChooseStep:
		return StepKindChoose
	case DelayStep:
		return StepKindDelay
	default:
		return ""
	}
}

// Kind reports the wire label of a supported Trigger value, or empty for invalid input.
func (trigger Trigger) Kind() TriggerKind {
	switch trigger.Body.(type) {
	case ObservationTrigger:
		return TriggerKindObservation
	case EntityEventTrigger:
		return TriggerKindEntityEvent
	case HeldStateTrigger:
		return TriggerKindHeldState
	case CronTrigger:
		return TriggerKindCron
	default:
		return ""
	}
}

// Kind reports the wire label of a supported Condition value, or empty for invalid input.
func (condition Condition) Kind() ConditionKind {
	switch condition.Body.(type) {
	case EntityStateCondition:
		return ConditionEntityState
	case TriggerCondition:
		return ConditionTrigger
	case AllCondition:
		return ConditionAll
	case AnyCondition:
		return ConditionAny
	case NotCondition:
		return ConditionNot
	default:
		return ""
	}
}
