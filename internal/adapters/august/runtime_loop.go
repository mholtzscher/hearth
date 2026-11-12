package august

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

var errPushOverflow = errors.New("august push ingress overflow")

const pushIngressCapacity = 128

type pushGeneration struct {
	worker  *lifecycle.WorkerHandle
	ingress chan pushReport
	targets []pushTarget
}

func (a *Adapter) startPush(ctx context.Context, targets []pushTarget) *pushGeneration {
	if len(targets) == 0 {
		return nil
	}
	generation := &pushGeneration{ingress: make(chan pushReport, pushIngressCapacity), targets: targets}
	push := newPushClient(a.client.http.Transport)
	generation.worker = lifecycle.StartWorker(ctx, func(ctx context.Context) error {
		return push.Run(ctx, targets, func(ctx context.Context, report pushReport) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case generation.ingress <- report:
				return nil
			default:
				return errPushOverflow
			}
		})
	})
	return generation
}

func (a *Adapter) pushTargets() []pushTarget {
	var targets []pushTarget
	for _, runtime := range a.devices {
		if runtime.online && runtime.entry != nil && runtime.entry.UserType == roleSuperuser &&
			validPushChannel(runtime.channel) {
			targets = append(targets, pushTarget{DeviceID: runtime.entry.ID, Channel: runtime.channel})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].DeviceID < targets[j].DeviceID })
	return targets
}

func (generation *pushGeneration) stop(ctx context.Context) {
	if generation != nil {
		_ = generation.worker.Stop(context.WithoutCancel(ctx))
	}
}

func (a *Adapter) applyPush(ctx context.Context, input pushReport) error {
	for _, runtime := range a.devices {
		if runtime.config != nil && runtime.config.LockID == string(input.DeviceID) {
			return a.applyDevicePush(ctx, runtime, input)
		}
	}
	return nil
}

func (a *Adapter) applyDevicePush(ctx context.Context, runtime *deviceRuntime, input pushReport) error {
	if err := acquire(ctx, runtime.gate); err != nil {
		return err
	}
	defer func() { <-runtime.gate }()
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
	a          *Adapter
	generation *pushGeneration
	poll       *time.Timer
	retry      *time.Timer
	retryC     <-chan time.Time
	backoff    time.Duration
}

// Run owns scheduled acquisition and one passive subscription generation, and joins it before returning.
func (a *Adapter) Run(ctx context.Context) error {
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
	defer func() { loop.generation.stop(ctx); loop.poll.Stop(); loop.retry.Stop() }()
	for {
		var input <-chan pushReport
		var ended <-chan struct{}
		if loop.generation != nil {
			input, ended = loop.generation.ingress, loop.generation.worker.Closed()
		}
		var err error
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ended:
			loop.subscriptionEnded(ctx)
		case message := <-input:
			select {
			case <-ended:
				loop.subscriptionEnded(ctx)
			default:
				err = a.applyPush(ctx, message)
				loop.backoff = time.Second
			}
		case <-loop.poll.C:
			err = loop.scheduled(ctx)
		case <-loop.retryC:
			err = loop.reconnect(ctx)
		}
		if err != nil && a.acquisitionFailed(ctx, err) {
			return err
		}
	}
}

func (loop *monitorLoop) scheduleRetry() {
	loop.retry.Reset(loop.backoff)
	loop.retryC = loop.retry.C
	const backoffMultiplier = 2
	loop.backoff = min(backoffMultiplier*loop.backoff, time.Minute)
}

func (loop *monitorLoop) subscriptionEnded(ctx context.Context) {
	err := loop.generation.worker.Wait(context.WithoutCancel(ctx))
	loop.a.pushFailed(ctx, err)
	loop.generation = nil // Queued reports cannot cross the resync boundary.
	loop.scheduleRetry()
}

func (loop *monitorLoop) scheduled(ctx context.Context) error {
	_, err := loop.a.cycle(ctx, false)
	targets := loop.a.pushTargets()
	if loop.generation != nil && !slices.Equal(targets, loop.generation.targets) {
		loop.generation.stop(ctx)
		loop.generation = nil
	}
	if loop.generation == nil && loop.retryC == nil {
		loop.generation = loop.a.startPush(ctx, targets)
	}
	loop.poll.Reset(loop.a.config.PollInterval)
	return err
}

func (loop *monitorLoop) reconnect(ctx context.Context) error {
	loop.retryC = nil
	complete, err := loop.a.cycle(ctx, true) // Reconcile before accepting a new generation's live reports.
	if complete && err == nil {
		loop.generation = loop.a.startPush(ctx, loop.a.pushTargets())
	} else {
		loop.scheduleRetry()
	}
	loop.poll.Reset(loop.a.config.PollInterval)
	return err
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
