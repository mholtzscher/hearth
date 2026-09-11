package zigbee2mqtt

// startOrdinaryEvent publishes one Entity Event as a runtime effect. Events
// never match a pending Command, so every valid report takes the ordinary
// path; the caller waits for the tracked effect to report its completion back
// through the coordinator before processing the next MQTT message.
func (coordinator *runtimeCoordinator) startOrdinaryEvent(candidate eventCandidate) {
	coordinator.startEffect(func() runtimeEvent {
		_, publishErr := coordinator.adapter.session.PublishEntityEvent(candidate.ctx, candidate.event)
		return ordinaryEventPublishFinished{result: candidate.result, err: publishErr}
	})
}
