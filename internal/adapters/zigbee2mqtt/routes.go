package zigbee2mqtt

import (
	"context"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func (z2m *Adapter) reportUnhealthy(ctx context.Context, reason string) error {
	z2m.disableRoutes()
	if err := z2m.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthUnhealthy, SourceObservedAt: time.Now().UTC(), ReasonCode: reason,
	}); err != nil {
		return &sessionOperationError{operation: "report unhealthy Zigbee2MQTT bridge", err: err}
	}
	return nil
}

func (z2m *Adapter) setConnection(
	generation uint64,
	connection mqttConnection,
	cancel context.CancelCauseFunc,
) {
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if z2m.generation == generation {
		z2m.connection = connection
		z2m.connectionCancel = cancel
	}
}

func (z2m *Adapter) clearConnection(generation uint64) {
	z2m.routeLifecycle.Lock()
	defer z2m.routeLifecycle.Unlock()
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if z2m.generation != generation {
		return
	}
	z2m.connection = nil
	z2m.connectionCancel = nil
	z2m.healthy = false
	z2m.routes = make(map[string]commandRoute)
	z2m.devices = make(map[string]runtimeDevice)
	z2m.cancelMatchersLocked()
}

func (z2m *Adapter) disableRoutes() {
	z2m.routeLifecycle.Lock()
	defer z2m.routeLifecycle.Unlock()
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	z2m.healthy = false
	z2m.routes = make(map[string]commandRoute)
	z2m.devices = make(map[string]runtimeDevice)
	z2m.cancelMatchersLocked()
}

func (z2m *Adapter) installSnapshot(generation uint64, snapshot routeSnapshot) {
	z2m.routeLifecycle.Lock()
	defer z2m.routeLifecycle.Unlock()
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if z2m.generation != generation {
		return
	}
	z2m.routeSerial++
	for entityID, route := range snapshot.routes {
		route.routeGeneration = z2m.routeSerial
		snapshot.routes[entityID] = route
	}
	z2m.cancelMatchersLocked()
	z2m.routes = snapshot.routes
	z2m.devices = snapshot.devices
	z2m.healthy = true
}

func (z2m *Adapter) cancelMatchersLocked() {
	for entityID, matcher := range z2m.matchers {
		delete(z2m.matchers, entityID)
		close(matcher.canceled)
	}
}

func (z2m *Adapter) isHealthy(generation uint64) bool {
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	return z2m.generation == generation && z2m.healthy
}
