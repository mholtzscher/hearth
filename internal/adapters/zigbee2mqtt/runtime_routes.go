package zigbee2mqtt

import "time"

func (coordinator *runtimeCoordinator) activateRoutes(event routesActivated) {
	if event.generation < coordinator.generation {
		event.result <- routeActivationResult{err: errStaleRuntimeGeneration}
		return
	}
	coordinator.dispatchable = false
	coordinator.invalidateAttempts(event.generation, 0)
	coordinator.generation = event.generation
	coordinator.routeRevision++
	coordinator.connection = event.connection
	coordinator.disconnect = event.disconnect
	coordinator.routes = make(map[string]commandRoute, len(event.snapshot.routes))
	for entityID, route := range event.snapshot.routes {
		route.connectionGeneration = coordinator.generation
		route.routeGeneration = coordinator.routeRevision
		coordinator.routes[entityID] = route
	}
	coordinator.dispatchable = true
	event.result <- routeActivationResult{revision: coordinator.routeRevision}
}

func (coordinator *runtimeCoordinator) invalidateRoutes(event routesInvalidated) {
	if event.generation < coordinator.generation {
		event.result <- nil
		return
	}
	coordinator.dispatchable = false
	coordinator.invalidateAttempts(event.generation, coordinator.routeRevision)
	coordinator.generation = event.generation
	coordinator.connection = nil
	coordinator.disconnect = nil
	clear(coordinator.routes)
	event.result <- nil
}

func (coordinator *runtimeCoordinator) invalidateAttempts(newGeneration, oldRevision uint64) {
	for _, attempt := range coordinator.attempts {
		if attempt.phase == commandPublishingLinked || attempt.phase == commandPublishingFallback {
			if attempt.cancelMQTT != nil {
				attempt.cancelMQTT()
			}
			continue
		}
		if attempt.generation == newGeneration && oldRevision != 0 && attempt.routeRevision != oldRevision {
			continue
		}
		if attempt.cancelMQTT != nil {
			attempt.cancelMQTT()
		}
		if attempt.evidence == nil && time.Now().Before(attempt.deadline) {
			coordinator.respondUnavailable(attempt)
		}
		coordinator.finishBeforeLink(attempt)
	}
}

func (coordinator *runtimeCoordinator) attemptRouteIsCurrent(attempt *commandAttempt) bool {
	if !coordinator.dispatchable || attempt.generation != coordinator.generation ||
		attempt.routeRevision != coordinator.routeRevision {
		return false
	}
	route, exists := coordinator.routes[attempt.command.EntityID]
	return exists && route.ieeeAddress == attempt.route.ieeeAddress &&
		route.entity.plan.Descriptor.Key == attempt.route.entity.plan.Descriptor.Key
}
