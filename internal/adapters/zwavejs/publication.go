package zwavejs

import (
	"log/slog"
	"strconv"
)

// publicationQueueLimit bounds pending batches, in addition to one active batch.
const publicationQueueLimit = 1024

type publicationQueueOverflowError struct{}

func (*publicationQueueOverflowError) Error() string {
	return "zwavejs: Observation publication queue reached " + strconv.Itoa(publicationQueueLimit) + " batches"
}

// startPublicationWorker starts the generation's only live Observation worker.
// Ordinary and linked batches share its FIFO. Startup publishes separately while
// live Events remain buffered, so a large snapshot cannot overflow this queue.
func (coordinator *runtimeCoordinator) startPublicationWorker(scope *generationScope) {
	scope.effects.Add(1)
	coordinator.effects.Go(func() {
		defer scope.effects.Done()
		for {
			select {
			case <-scope.ctx.Done():
				return
			case publish := <-scope.publications:
				if scope.ended() {
					return
				}
				// Delivery also selects on scope cancellation, allowing teardown to
				// join this worker even when the coordinator's mailbox is full.
				coordinator.sendScopedCompletion(scope, publish())
			}
		}
	})
}

// enqueuePublication never blocks the coordinator. Overflow ends the generation
// and cancels its active publication before reconnect obtains fresh State.
// Callers must stop preparing work when this returns false.
func (coordinator *runtimeCoordinator) enqueuePublication(scope *generationScope, publish func() runtimeEvent) bool {
	if scope == nil || scope.ended() {
		return false
	}
	select {
	case scope.publications <- publish:
		return true
	default:
		coordinator.adapter.logger.WarnContext(
			coordinator.ctx,
			"Z-Wave Observation publication queue overflowed",
			slog.String(eventKey, "adapter.publication_queue_overflowed"),
			slog.Int("batch_limit", publicationQueueLimit),
		)
		coordinator.dropGeneration(&publicationQueueOverflowError{})
		return false
	}
}
