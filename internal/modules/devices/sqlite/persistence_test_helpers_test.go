package sqlite //nolint:testpackage // Shared fixtures for the package-private SQLite persistence tests.

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// testAdapterLeaseDuration is the Adapter runtime lease window the devices
// domain applies in Service.ClaimAdapterRuntime and Service.RecordAdapterHeartbeat.
// The domain package keeps its own copy private, so the SQLite tests state the
// window they expect instead of reaching across the package boundary.
const testAdapterLeaseDuration = 15 * time.Second

// testEntityEventDeleteBatchSize mirrors the private devices Entity Event
// retention batch policy. The multi-batch sweep test seeds more than two full
// batches, so this value must track the retention policy it exercises.
const testEntityEventDeleteBatchSize = 500

// Fixture identities handed to Service.ExecuteCommand so a test can assert the
// caller-supplied Command and Correlation identities survive persistence.
const (
	commandTestEntityID      = devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestID            = devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestCorrelationID = devices.CorrelationID("cor_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	commandTestObservationID = devices.ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

// newTestService assembles a devices Service over one SQLite repository. The
// SQLite repository implements every persistence capability, so a single
// repository supplies the whole Stores graph.
func newTestService(
	repository *DeviceRepository,
	sender devices.CommandSender,
	catalog *devices.TypeCatalog,
	dependencies devices.Dependencies,
) *devices.Service {
	return devices.NewService(DeviceStores(repository), sender, catalog, dependencies)
}

// newAvailabilityRequestID mints one canonical avl_ request identity for an
// availability batch. The SQLite repository validates the stored request
// identity, so the fixture keeps the canonical UUIDv7 shape.
func newAvailabilityRequestID(t availabilityTestT) string {
	t.Helper()
	value, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return "avl_" + value.String()
}

// commandSenderFunc adapts a function to the devices CommandSender seam so a
// test can script the exact acceptance one dispatch returns.
type commandSenderFunc func(
	context.Context, string, devices.RuntimeID, devices.CommandRequest,
) (devices.CommandAcceptance, error)

func (send commandSenderFunc) Send(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	request devices.CommandRequest,
) (devices.CommandAcceptance, error) {
	return send(ctx, adapterID, runtimeID, request)
}

// commandValidationDependencies panics when validation reads the command clock
// or mints an identity, so a validation-only path cannot quietly do either.
func commandValidationDependencies() devices.Dependencies {
	return devices.Dependencies{
		Now:              func() time.Time { panic("validation read the command clock") },
		NewCommandID:     func() (devices.CommandID, error) { panic("validation generated a command ID") },
		NewCorrelationID: func() (devices.CorrelationID, error) { panic("validation generated a correlation ID") },
	}
}

// recordingDeviceFactNotifier counts the wake hints a service sends so a test
// can prove exactly how many queued facts were announced.
type recordingDeviceFactNotifier struct {
	mutex    sync.Mutex
	notified int
}

func (notifier *recordingDeviceFactNotifier) NotifyPendingDeviceFacts() {
	notifier.mutex.Lock()
	notifier.notified++
	notifier.mutex.Unlock()
}

func (notifier *recordingDeviceFactNotifier) count() int {
	notifier.mutex.Lock()
	defer notifier.mutex.Unlock()
	return notifier.notified
}
