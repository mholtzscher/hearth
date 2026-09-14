package lifecycle

import "context"

// WorkerHandle owns cancellation and completion of one worker, not its scheduling
// or fault policy. Use StartWorker; the zero value is not usable. Do not copy.
// All methods are safe for concurrent use.
type WorkerHandle struct {
	cancel context.CancelFunc
	closed chan struct{}
	err    error // Published by closing closed; read only after joining.
}

// StartWorker runs work once with a child of ctx. The worker must join any
// goroutines it starts before returning. Panics are not recovered.
func StartWorker(ctx context.Context, work func(context.Context) error) *WorkerHandle {
	workerContext, cancel := context.WithCancel(ctx)
	worker := &WorkerHandle{cancel: cancel, closed: make(chan struct{})}
	go func() {
		defer close(worker.closed)
		defer cancel()
		worker.err = work(workerContext)
	}()
	return worker
}

// Stop requests cancellation and waits for the worker to return. Cancellation is
// idempotent. A wait timeout leaves the worker tracked and cancellation in effect;
// keep its dependencies alive until Closed closes.
func (worker *WorkerHandle) Stop(ctx context.Context) error {
	worker.cancel()
	return worker.Wait(ctx)
}

// Wait returns the worker's terminal error unchanged, or ctx.Err() if waiting
// ends first. It does not cancel the worker. If both are ready, either may win.
func (worker *WorkerHandle) Wait(ctx context.Context) error {
	select {
	case <-worker.closed:
		return worker.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Closed closes after the worker and its deferred cleanup return, not when
// cancellation is requested. Closing also publishes the terminal result to Wait.
func (worker *WorkerHandle) Closed() <-chan struct{} {
	return worker.closed
}
