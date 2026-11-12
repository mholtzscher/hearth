package esphome

import (
	"context"
	"errors"
	"log/slog"
	"sort"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

const maximumDevices = 64

// managedDevice keeps worker cancellation and joining with the supervisor, not the mDNS receiver.
type managedDevice struct {
	worker *worker
	handle *lifecycle.WorkerHandle
}

type deviceSupervisor struct {
	adapter   *Adapter
	ctx       context.Context
	owned     map[string]map[string]string
	active    map[MAC]managedDevice
	failures  chan error
	skipped   map[MAC]string
	pending   map[MAC]Device
	nextIndex int
}

func (a *Adapter) runDevices(ctx context.Context, owned map[string]map[string]string) error {
	runContext, cancel := context.WithCancel(ctx)
	supervisor := &deviceSupervisor{
		adapter: a, ctx: runContext, owned: owned, active: map[MAC]managedDevice{},
		failures: make(chan error, maximumDevices+1),
		skipped:  map[MAC]string{},
		pending:  map[MAC]Device{},
	}
	var source *lifecycle.WorkerHandle
	defer func() {
		cancel()
		if source != nil {
			_ = source.Stop(context.Background())
		}
		for _, current := range supervisor.active {
			_ = current.handle.Stop(context.Background())
		}
	}()
	a.router.seedOwned(owned)
	for _, device := range a.config.Static {
		_, blocked := a.config.Blocklist[device.MAC]
		if !blocked {
			supervisor.start(device)
		}
	}
	records := make(chan DiscoveryRecord, maximumDevices)
	if a.config.Discovery.Enabled {
		if a.config.Discovery.AllowInsecurePlaintext {
			a.logger.WarnContext(ctx, "discovery permits unencrypted devices on the trusted network",
				slog.String("event", "discovery.insecure_plaintext_allowed"))
		}
		source = lifecycle.StartWorker(runContext, func(sourceContext context.Context) error {
			return supervisor.browse(sourceContext, records)
		})
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-supervisor.failures:
			return err
		case record := <-records:
			if err := supervisor.discovered(record); err != nil {
				return err
			}
		}
	}
}

func (supervisor *deviceSupervisor) browse(ctx context.Context, records chan<- DiscoveryRecord) error {
	err := supervisor.adapter.discovery.Run(ctx, records)
	if ctx.Err() == nil {
		if err == nil {
			err = errors.New("discovery stopped unexpectedly")
		}
		supervisor.failures <- &discoveryError{err: err}
	}
	return err
}

func (supervisor *deviceSupervisor) start(device Device) {
	w := newWorker(supervisor.adapter, supervisor.nextIndex, device, supervisor.owned[device.MAC.BindingKey()])
	supervisor.nextIndex++
	entityIDs := make([]string, 0, len(w.owned))
	for _, entityID := range w.owned {
		entityIDs = append(entityIDs, entityID)
	}
	supervisor.adapter.router.assign(w, entityIDs)
	handle := lifecycle.StartWorker(supervisor.ctx, func(ctx context.Context) error {
		err := w.run(ctx)
		if err != nil {
			supervisor.failures <- err
		}
		return err
	})
	supervisor.active[device.MAC] = managedDevice{worker: w, handle: handle}
}

func (supervisor *deviceSupervisor) discovered(record DiscoveryRecord) error {
	// Explicit addresses remain authoritative, and discovery never creates a second worker for them.
	for _, configured := range supervisor.adapter.config.Static {
		if configured.MAC == record.MAC {
			return nil
		}
	}
	device, reason := supervisor.adapter.discoveredDevice(record)
	previous, exists := supervisor.active[record.MAC]
	if reason != "" {
		delete(supervisor.pending, record.MAC)
		if exists {
			if err := supervisor.stop(record.MAC); err != nil {
				return err
			}
		}
		if err := supervisor.reportSkipped(record.MAC, reason); err != nil {
			return err
		}
		supervisor.admitPending()
		return nil
	}
	delete(supervisor.skipped, record.MAC)
	if exists && previous.worker.device.Address == device.Address && previous.worker.device.Port == device.Port &&
		previous.worker.device.Transport == device.Transport {
		return nil
	}
	if exists {
		if err := supervisor.stop(record.MAC); err != nil {
			return err
		}
		if err := reportUnavailable(
			supervisor.ctx,
			supervisor.adapter.session,
			supervisor.owned[record.MAC.BindingKey()],
			reasonDeviceUnreachable,
		); err != nil {
			return err
		}
	}
	if supervisor.queueWhenFull(device) {
		return nil
	}
	supervisor.start(device)
	delete(supervisor.pending, record.MAC)
	return nil
}

func (supervisor *deviceSupervisor) queueWhenFull(device Device) bool {
	if len(supervisor.active) < maximumDevices {
		return false
	}
	// Keep a bounded waiting set because the browser suppresses unchanged advertisements.
	if _, waiting := supervisor.pending[device.MAC]; waiting || len(supervisor.pending) < maximumDevices {
		supervisor.pending[device.MAC] = device
	}
	supervisor.adapter.logger.WarnContext(supervisor.ctx, "discovered device exceeds runtime limit",
		slog.String("event", "discovery.device_limit"))
	return true
}

func (supervisor *deviceSupervisor) admitPending() {
	keys := make([]MAC, 0, len(supervisor.pending))
	for mac := range supervisor.pending {
		keys = append(keys, mac)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, mac := range keys {
		if len(supervisor.active) == maximumDevices {
			return
		}
		supervisor.start(supervisor.pending[mac])
		delete(supervisor.pending, mac)
	}
}

func (supervisor *deviceSupervisor) stop(mac MAC) error {
	current := supervisor.active[mac]
	if err := current.handle.Stop(context.Background()); err != nil {
		return err
	}
	// The worker has joined, so ownership and availability are no longer accessed concurrently.
	supervisor.owned[mac.BindingKey()] = current.worker.owned
	supervisor.adapter.router.release(current.worker)
	delete(supervisor.active, mac)
	return nil
}

func (supervisor *deviceSupervisor) reportSkipped(mac MAC, reason string) error {
	if supervisor.skipped[mac] == reason {
		return nil
	}
	if len(supervisor.skipped) < maximumDevices {
		supervisor.skipped[mac] = reason
	}
	supervisor.adapter.logger.WarnContext(supervisor.ctx, "discovered device skipped",
		slog.String("event", "discovery.device_skipped"), slog.String("binding_key", mac.BindingKey()),
		slog.String("reason_code", reason))
	return reportUnavailable(supervisor.ctx, supervisor.adapter.session, supervisor.owned[mac.BindingKey()], reason)
}
