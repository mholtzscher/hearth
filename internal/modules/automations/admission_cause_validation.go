package automations

// ValidateAdmissionCause checks owned provenance against its matched Trigger snapshots.
// Historical Facts receive structural validation, never freshness validation.
func ValidateAdmissionCause(cause AdmissionCause, matched []Trigger) error {
	switch cause := cause.(type) {
	case ManualCause:
		if len(matched) != 0 {
			return invalid("manual admission carries matched Triggers")
		}
	case DeviceFactCause:
		return validateFactCauseMatches(cause.Fact, matched)
	case HeldStateCause:
		return validateHeldCauseMatches(cause.Evidence, matched)
	case ScheduleCause:
		if len(matched) == 0 {
			return invalid("schedule admission lacks matched Triggers")
		}
		for _, trigger := range matched {
			switch trigger.Body.(type) {
			case CronTrigger:
			case ObservationTrigger, EntityEventTrigger, HeldStateTrigger:
				return invalid("schedule admission matched non-Cron Trigger")
			default:
				return invalid("unsupported matched Trigger")
			}
		}
	default:
		return invalid("unsupported admission cause %T", cause)
	}
	return nil
}

func validateFactCauseMatches(fact DeviceFact, matched []Trigger) error {
	if err := ValidateDeviceFact(fact); err != nil {
		return invalid("retained Fact: %v", err)
	}
	if len(matched) == 0 {
		return invalid("Fact admission lacks matched Triggers")
	}
	for _, trigger := range matched {
		if err := validateFactCauseTrigger(fact, trigger); err != nil {
			return err
		}
	}
	return nil
}

func validateFactCauseTrigger(fact DeviceFact, trigger Trigger) error {
	switch fact := fact.(type) {
	case ObservationFact:
		body, ok := trigger.Body.(ObservationTrigger)
		if !ok {
			return invalid("Observation Fact matched wrong Trigger family or Entity")
		}
		matches, err := matchObservationTrigger(&fact, &body)
		if err != nil {
			return invalid("retained Observation Trigger %q: %v", trigger.ID, err)
		}
		if !matches {
			return invalid("Observation Fact does not match retained Trigger %q", trigger.ID)
		}
	case EntityEventFact:
		body, ok := trigger.Body.(EntityEventTrigger)
		if !ok || body.EntityID != fact.EntityID || body.EventName != fact.Name {
			return invalid("Entity Event Fact matched wrong Trigger family or Entity")
		}
	default:
		return invalid("unsupported retained Fact")
	}
	return nil
}

func validateHeldCauseMatches(evidence HeldStateEvidence, matched []Trigger) error {
	if len(matched) != 1 || matched[0].ID != evidence.TriggerID {
		return invalid("held-state evidence disagrees with matched Trigger")
	}
	body, ok := matched[0].Body.(HeldStateTrigger)
	if !ok || evidence.StartedAt.IsZero() || evidence.DueAt.IsZero() {
		return invalid("invalid held-state evidence")
	}
	duration, err := HeldStateDuration(body.ForSeconds)
	if err != nil || !evidence.DueAt.Equal(evidence.StartedAt.Add(duration)) {
		return invalid("held-state window disagrees with Trigger duration")
	}
	return nil
}
