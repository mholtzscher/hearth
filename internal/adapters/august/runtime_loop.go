package august

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

var (
	errPushOverflow = errors.New("august push ingress overflow")
	errPushResync   = errors.New("august push requires account reassessment")
)

const pushIngressCapacity = 128

type pushGeneration struct {
	worker  *lifecycle.WorkerHandle
	targets []pushTarget
	applied atomic.Bool
}

func (a *Adapter) startPush(ctx context.Context, targets []pushTarget) *pushGeneration {
	if len(targets) == 0 {
		return nil
	}
	generation := &pushGeneration{targets: targets}
	generation.worker = lifecycle.StartWorker(ctx, func(ctx context.Context) error {
		return a.runPushGeneration(ctx, targets, &generation.applied)
	})
	return generation
}

type pushApplications struct {
	queues  map[lockID]chan pushReport
	budget  chan struct{}
	errors  chan error
	workers sync.WaitGroup
}

func (a *Adapter) runPushGeneration(ctx context.Context, targets []pushTarget, applied *atomic.Bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	applications := &pushApplications{queues: make(map[lockID]chan pushReport, len(targets)),
		budget: make(chan struct{}, pushIngressCapacity), errors: make(chan error, len(targets))}
	for _, target := range targets {
		for _, runtime := range a.devices {
			if runtime.config != nil && runtime.config.LockID == string(target.DeviceID) {
				queue := make(chan pushReport, pushIngressCapacity)
				applications.queues[target.DeviceID] = queue
				applications.workers.Go(
					func() { a.runPushApplication(ctx, cancel, runtime, queue, applications, applied) },
				)
				break
			}
		}
	}
	err := newPushClient(a.client.http.Transport).Run(ctx, targets, applications.deliver)
	cancel()
	applications.workers.Wait()
	close(applications.errors)
	for workerErr := range applications.errors {
		err = errors.Join(err, workerErr)
	}
	return err
}

func (applications *pushApplications) deliver(ctx context.Context, report pushReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case applications.budget <- struct{}{}:
	default:
		return errPushOverflow
	}
	select {
	case applications.queues[report.DeviceID] <- report:
		return nil
	case <-ctx.Done():
		<-applications.budget
		return ctx.Err()
	}
}

func (a *Adapter) runPushApplication(ctx context.Context, cancel context.CancelFunc, runtime *deviceRuntime,
	queue <-chan pushReport, applications *pushApplications, applied *atomic.Bool) {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case input := <-queue:
			if ctx.Err() != nil {
				return
			}
			err := a.applyDevicePush(ctx, runtime, input)
			<-applications.budget
			if err != nil {
				applications.errors <- err
				cancel()
				return
			}
			applied.Store(true)
		}
	}
}

func (a *Adapter) pushTargets() []pushTarget {
	var targets []pushTarget
	for _, runtime := range a.devices {
		if target := runtime.pushTarget.Load(); target != nil {
			targets = append(targets, *target)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].DeviceID < targets[j].DeviceID })
	return targets
}

// Called under the lock gate; readers never wait for provider I/O or publication.
func (runtime *deviceRuntime) updatePushTarget() {
	if runtime.online && runtime.entry != nil && runtime.entry.UserType == roleSuperuser &&
		validPushChannel(runtime.channel) {
		runtime.pushTarget.Store(&pushTarget{DeviceID: runtime.entry.ID, Channel: runtime.channel})
	} else {
		runtime.pushTarget.Store(nil)
	}
}

func (generation *pushGeneration) stop(ctx context.Context) {
	if generation != nil {
		_ = generation.worker.Stop(context.WithoutCancel(ctx))
	}
}

func (a *Adapter) applyDevicePush(ctx context.Context, runtime *deviceRuntime, input pushReport) error {
	if err := acquire(ctx, runtime.gate); err != nil {
		return err
	}
	defer func() { runtime.updatePushTarget(); <-runtime.gate }()
	if a.needsAssessment.Load() != 0 {
		return errPushResync
	}
	if runtime.entry == nil || runtime.entry.ID != input.DeviceID || runtime.entry.UserType != roleSuperuser {
		return nil
	}
	if input.Report.Online != nil && !*input.Report.Online {
		return a.unavailable(ctx, runtime, "device_offline")
	}
	if needsRecovery(runtime) {
		if !runtime.pushRecoveryUsed {
			runtime.pushRecoveryUsed = true
			if err := a.refreshDetail(ctx, runtime); err != nil {
				return err
			}
		}
	}
	if !runtime.online {
		return nil
	}
	var keys []string
	if input.Report.Lock != nil && runtime.fields[keyLock] != nil && runtime.fields[keyLock].available {
		keys = append(keys, keyLock)
	}
	if input.Report.DoorOpen != nil && runtime.fields[keyDoorOpen] != nil && runtime.fields[keyDoorOpen].available {
		keys = append(keys, keyDoorOpen)
	}
	if len(keys) == 0 {
		return nil
	}
	return a.publish(ctx, runtime, input.Report, keys)
}

type monitorLoop struct {
	a            *Adapter
	generation   *pushGeneration
	poll         *time.Timer
	retry        *time.Timer
	retryC       <-chan time.Time
	backoff      time.Duration
	acquisition  *lifecycle.WorkerHandle
	complete     bool
	reconnecting bool
	retryDue     bool
}

// Run owns scheduled acquisition and one passive subscription generation, and joins it before returning.
func (a *Adapter) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := a.loadMappings(ctx); err != nil {
		return err
	}
	if _, err := a.cycle(ctx, true); err != nil && a.acquisitionFailed(ctx, err) {
		return err
	}
	loop := monitorLoop{a: a, generation: a.startPush(ctx, a.pushTargets()), poll: time.NewTimer(a.config.PollInterval),
		retry: time.NewTimer(time.Hour), backoff: time.Second}
	if !loop.retry.Stop() {
		<-loop.retry.C
	}
	defer func() {
		cancel()
		loop.generation.stop(ctx)
		if loop.acquisition != nil {
			_ = loop.acquisition.Stop(context.WithoutCancel(ctx))
		}
		loop.poll.Stop()
		loop.retry.Stop()
	}()
	for {
		if err := loop.step(ctx); err != nil && a.acquisitionFailed(ctx, err) {
			return err
		}
	}
}

func (loop *monitorLoop) step(ctx context.Context) error {
	var ended, acquired <-chan struct{}
	if loop.generation != nil {
		ended = loop.generation.worker.Closed()
	}
	if loop.acquisition != nil {
		acquired = loop.acquisition.Closed()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ended:
		return loop.subscriptionEnded(ctx)
	case <-loop.poll.C:
		if loop.acquisition == nil {
			loop.startAcquisition(ctx, false)
		}
	case <-loop.retryC:
		loop.retryC = nil
		if loop.acquisition == nil {
			loop.startAcquisition(ctx, true)
		} else {
			loop.retryDue = true
		}
	case <-acquired:
		return loop.finishAcquisition(ctx)
	}
	return nil
}

func (loop *monitorLoop) scheduleRetry() {
	loop.retry.Reset(loop.backoff)
	loop.retryC = loop.retry.C
	const backoffMultiplier = 2
	loop.backoff = min(backoffMultiplier*loop.backoff, time.Minute)
}

func (loop *monitorLoop) subscriptionEnded(ctx context.Context) error {
	err := loop.generation.worker.Wait(context.WithoutCancel(ctx))
	loop.a.pushFailed(ctx, err)
	if loop.generation.applied.Load() {
		loop.backoff = time.Second
	}
	loop.generation = nil // Queued reports cannot cross the resync boundary.
	loop.scheduleRetry()
	var rejected *adapter.RegistrationRejectedError
	if errors.Is(err, adapter.ErrClosed) || errors.Is(err, adapter.ErrRuntimeFenced) || errors.As(err, &rejected) {
		return err
	}
	return nil
}

func (loop *monitorLoop) startAcquisition(ctx context.Context, reconnect bool) {
	loop.reconnecting = reconnect
	loop.complete = false
	loop.acquisition = lifecycle.StartWorker(ctx, func(ctx context.Context) error {
		var err error
		loop.complete, err = loop.a.cycle(ctx, reconnect)
		return err
	})
}

func (loop *monitorLoop) finishAcquisition(ctx context.Context) error {
	err := loop.acquisition.Wait(context.WithoutCancel(ctx))
	loop.acquisition = nil
	needReconnect := false
	if loop.reconnecting {
		if loop.complete && err == nil {
			loop.generation = loop.a.startPush(ctx, loop.a.pushTargets())
		} else {
			loop.scheduleRetry()
		}
	} else {
		needReconnect = loop.reconcileTargets(ctx)
	}
	loop.poll.Reset(loop.a.config.PollInterval)
	if loop.retryDue || needReconnect {
		loop.retryDue = false
		loop.poll.Stop()
		loop.startAcquisition(ctx, true)
	}
	return err
}

func (loop *monitorLoop) reconcileTargets(ctx context.Context) bool {
	targets := loop.a.pushTargets()
	if loop.generation != nil && !slices.Equal(targets, loop.generation.targets) {
		loop.generation.stop(ctx)
		loop.generation = nil
	}
	if loop.generation == nil && loop.retryC == nil && !loop.retryDue {
		return len(targets) > 0
	}
	return false
}

func (a *Adapter) acquisitionFailed(ctx context.Context, err error) bool {
	var rejected *adapter.RegistrationRejectedError
	if ctx.Err() != nil || errors.Is(err, adapter.ErrClosed) || errors.Is(err, adapter.ErrRuntimeFenced) ||
		errors.As(err, &rejected) {
		return true
	}
	a.logger.WarnContext(
		ctx,
		"August evidence publication failed",
		slog.String("event", "august.publication_failed"),
		slog.String("error_code", "sdk_failed"),
	)
	return false
}

func (a *Adapter) pushFailed(ctx context.Context, err error) {
	code := failureCode(err)
	if errors.Is(err, errPushOverflow) {
		code = "ingress_overflow"
	}
	a.logger.WarnContext(
		ctx,
		"August subscription ended",
		slog.String("event", "august.push_failed"),
		slog.String("error_code", code),
	)
}
