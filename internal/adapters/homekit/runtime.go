package homekit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	reloadDelay   = 2 * time.Second
	recoveryDelay = time.Second
)

// RuntimeConfig contains the validated settings used by the bridge itself.
type RuntimeConfig struct {
	DataDir, BridgeName, DisplayUnit string
	ListenPort                       int
	Interfaces                       []string
	ResyncInterval                   time.Duration
}

type runtimeCore interface {
	commandExecutor
	ReadInventory(context.Context) (Inventory, error)
}

// Runtime coordinates one Session, one HAP generation, and one Fact generation.
// The coordinator alone commits inventory and changes the advertised topology.
type Runtime struct {
	config  RuntimeConfig
	session *adapter.Session
	core    runtimeCore
	facts   *FactSource
	disk    *store
	logger  *slog.Logger

	mu        sync.RWMutex // protects inventory and reader access to the active HAP server
	inventory Inventory
	published Inventory // descriptor inputs at the last successful HAP publication
	hap       *runningHAP
	runner    *commandRunner
	accessors []*accessoryBuilder
	mutations chan bridgeMutation
}

// NewRuntime opens the bridge's private identity store after the Session claim.
func NewRuntime(
	config RuntimeConfig, session *adapter.Session, core *CoreClient, js jetstream.JetStream, logger *slog.Logger,
) (*Runtime, error) {
	if config.ResyncInterval <= 0 || config.ListenPort <= 0 || session == nil || core == nil || logger == nil {
		return nil, errors.New("HomeKit runtime requires resolved configuration and Core dependencies")
	}
	facts, err := NewFactSource(js, logger)
	if err != nil {
		return nil, err
	}
	disk, err := openStore(config.DataDir)
	if err != nil {
		return nil, err
	}
	if err = cleanRetiredPairings(config.DataDir, disk.snapshot().PairEpoch); err != nil {
		return nil, err
	}
	return &Runtime{
		config: config, session: session, core: core, facts: facts, disk: disk,
		logger: logger, mutations: make(chan bridgeMutation),
	}, nil
}

// Run starts the published bridge and its management Functions and supervises them
// until the caller cancels. It joins owned work before returning to Session assembly.
func (r *Runtime) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.runner = newCommandRunner(ctx, r.core, r.lookup, r.logger)
	defer r.stopHAP()
	defer r.runner.close()
	feed, err := r.openInitialFeed(ctx)
	if err != nil {
		return err
	}
	defer func() { stopFeed(feed) }()
	if _, err = r.refresh(ctx); err != nil {
		_ = r.reportHealth(ctx, healthCore)
		return err
	}
	if err = r.startHAP(ctx); err != nil {
		_ = r.reportHealth(ctx, healthHAP)
		return err
	}
	functions, err := r.session.StartFunctionServer(ctx, bridgeFunctions(r))
	if err != nil {
		return fmt.Errorf("register HomeKit Functions: %w", err)
	}
	// Cancel pending mutation waiters before joining the SDK's Function server.
	defer func() {
		cancel()
		_ = functions.Close(context.WithoutCancel(ctx))
	}()
	if err = r.reportHealth(ctx, ""); err != nil {
		return err
	}
	r.coordinate(ctx, &feed)
	return ctx.Err()
}

func (r *Runtime) openInitialFeed(ctx context.Context) (*liveFactFeed, error) {
	for {
		feed, err := r.openFeed(ctx)
		if err == nil {
			return feed, nil
		}
		_ = r.reportHealth(ctx, healthFacts)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(recoveryDelay):
		}
	}
}

type liveFactFeed struct {
	feed   *FactFeed
	cancel context.CancelFunc
	done   chan struct{}
	facts  chan ObservationFact
}

func (r *Runtime) openFeed(ctx context.Context) (*liveFactFeed, error) {
	feed, err := r.facts.Open(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	live := &liveFactFeed{feed: feed, cancel: cancel, done: make(chan struct{}), facts: make(chan ObservationFact)}
	go func() {
		defer close(live.done)
		_ = feed.Run(ctx, func(ctx context.Context, fact ObservationFact) error {
			select {
			case live.facts <- fact:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	return live, nil
}

func stopFeed(live *liveFactFeed) {
	if live == nil {
		return
	}
	live.cancel()
	<-live.done
	ctx, cancel := context.WithTimeout(context.Background(), recoveryDelay)
	defer cancel()
	_ = live.feed.Close(ctx)
}

func (r *Runtime) lookup(id string) Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if time.Since(r.inventory.ReadAt) > 3*r.config.ResyncInterval {
		return Entity{}
	}
	return r.inventory.Entities[id]
}

func (r *Runtime) inventorySnapshot() Inventory {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if time.Since(r.inventory.ReadAt) > 3*r.config.ResyncInterval {
		return Inventory{}
	}
	snapshot := r.inventory
	snapshot.Entities = maps.Clone(snapshot.Entities)
	snapshot.Devices = maps.Clone(snapshot.Devices)
	return snapshot
}

func (r *Runtime) exposureSnapshot() storeFile { return r.disk.snapshot() }

func (r *Runtime) pairingStatus() (int, SetupIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.hap == nil || !r.hap.active() {
		return 0, SetupIdentity{}, errReloadFailed
	}
	count, err := r.hap.controllerCount()
	return count, r.hap.setup, err
}

func (r *Runtime) refresh(ctx context.Context) (bool, error) {
	next, err := r.core.ReadInventory(ctx)
	if err != nil {
		return false, err
	}
	r.mu.Lock()
	r.inventory = next
	r.mu.Unlock()
	for _, item := range r.accessors {
		for _, binding := range item.bindings {
			binding.update(next.Entities[binding.entity])
		}
	}
	return topologyChanged(r.disk.snapshot().Exposures, r.published, next), nil
}

func topologyChanged(exposures map[string]Exposure, before, after Inventory) bool {
	for _, exposure := range exposures {
		if before.Devices[exposure.DeviceID] != after.Devices[exposure.DeviceID] {
			return true
		}
		for _, assignment := range exposure.Entities {
			old, current := before.Entities[assignment.EntityID], after.Entities[assignment.EntityID]
			if old.ID != current.ID || old.DeviceID != current.DeviceID || old.Type != current.Type ||
				!bytes.Equal(old.Support, current.Support) {
				return true
			}
			if exposure.Kind == KindThermostat && hasThermostatHumidity(old) != hasThermostatHumidity(current) {
				return true
			}
		}
	}
	return false
}

func (r *Runtime) applyFact(fact ObservationFact) {
	var bindings []characteristicBinding
	for _, item := range r.accessors {
		for _, binding := range item.bindings {
			if binding.entity == fact.EntityID {
				bindings = append(bindings, binding)
			}
		}
	}
	if len(bindings) == 0 {
		return
	}
	r.mu.Lock()
	applied := r.inventory.applyFact(fact)
	entity := r.inventory.Entities[fact.EntityID]
	r.mu.Unlock()
	if applied {
		for _, binding := range bindings {
			binding.update(entity)
		}
	}
}

func (r *Runtime) startHAP(ctx context.Context) error {
	data := r.disk.snapshot()
	exposures := make([]Exposure, 0, len(data.Exposures))
	for _, exposure := range data.Exposures {
		exposures = append(exposures, exposure)
	}
	slices.SortFunc(exposures, func(a, b Exposure) int {
		switch {
		case a.AID < b.AID:
			return -1
		case a.AID > b.AID:
			return 1
		default:
			return 0
		}
	})
	generation := make([]*accessoryBuilder, 0, len(exposures))
	children := make([]*accessory.A, 0, len(exposures))
	for _, exposure := range exposures {
		b, err := buildAccessory(
			exposure,
			r.inventory,
			r.config.DisplayUnit,
			r.lookup,
			func(aid uint64, intent writeIntent) int {
				if !r.disk.hasAssignment(exposure, intent.EntityID) {
					return hapCommunicationFailure
				}
				return r.runner.submit(aid, intent)
			},
		)
		if err != nil {
			return fmt.Errorf("build HomeKit accessory: %w", err)
		}
		generation = append(generation, b)
		children = append(children, b.accessory)
	}
	run, err := launchHAP(ctx, r.config, data, children)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.hap, r.accessors = run, generation
	r.published = Inventory{Devices: maps.Clone(r.inventory.Devices), Entities: maps.Clone(r.inventory.Entities)}
	r.mu.Unlock()
	return nil
}

func (r *Runtime) stopHAP() {
	r.mu.Lock()
	run := r.hap
	r.hap, r.accessors = nil, nil
	r.mu.Unlock()
	if run != nil {
		run.stop()
	}
}

type bridgeMutation struct {
	desired Exposure
	remove  bool
	reset   bool
	reply   chan mutationResult
	ctx     context.Context
}

type mutationResult struct {
	exposure Exposure
	changed  bool
	err      error
}

func (r *Runtime) changeExposure(ctx context.Context, desired Exposure, remove bool) (Exposure, bool, error) {
	result := r.requestMutation(bridgeMutation{ctx: ctx, desired: desired, remove: remove})
	return result.exposure, result.changed, result.err
}

func (r *Runtime) resetPairing(ctx context.Context) error {
	return r.requestMutation(bridgeMutation{ctx: ctx, reset: true}).err
}

func (r *Runtime) requestMutation(request bridgeMutation) mutationResult {
	request.reply = make(chan mutationResult, 1)
	select {
	case r.mutations <- request:
	case <-request.ctx.Done():
		return mutationResult{err: request.ctx.Err()}
	}
	select {
	case result := <-request.reply:
		return result
	case <-request.ctx.Done():
		return mutationResult{err: request.ctx.Err()}
	}
}

func (r *Runtime) commitExposure(request bridgeMutation) mutationResult {
	if err := request.ctx.Err(); err != nil {
		return mutationResult{err: err}
	}
	data := r.disk.snapshot()
	old, exists := data.Exposures[request.desired.DeviceID]
	if request.remove {
		if !exists {
			return mutationResult{}
		}
		return mutationResult{changed: true, err: r.disk.unexpose(old.DeviceID)}
	}
	inventory := r.inventorySnapshot()
	if inventory.ReadAt.IsZero() {
		return mutationResult{err: &PlanError{Reason: reasonCoreUnreachable}}
	}
	if err := ValidateExposure(request.desired, inventory); err != nil {
		return mutationResult{err: err}
	}
	if exists && old.Kind == request.desired.Kind && old.Name == request.desired.Name &&
		reflect.DeepEqual(old.Entities, request.desired.Entities) {
		return mutationResult{exposure: old}
	}
	exposure, err := r.disk.expose(request.desired)
	return mutationResult{exposure: exposure, changed: true, err: err}
}

func (r *Runtime) rotatePairing(ctx context.Context) mutationResult {
	r.stopHAP()
	err := r.disk.rotatePairing()
	if err == nil {
		if removeErr := cleanRetiredPairings(r.config.DataDir, r.disk.snapshot().PairEpoch); removeErr != nil {
			err = errors.Join(errReloadFailed, removeErr)
		}
	}
	if !errors.Is(err, errStoreDurabilityUncertain) {
		if launchErr := r.startHAP(ctx); launchErr != nil {
			err = errors.Join(err, errReloadFailed, launchErr)
		}
	}
	return mutationResult{err: err}
}

func cleanRetiredPairings(dir string, active uint64) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list HAP pairing generations: %w", err)
	}
	for _, entry := range entries {
		epoch, ok := pairingEpoch(entry.Name())
		if !ok || epoch >= active {
			continue
		}
		if removeErr := os.RemoveAll(pairingPath(dir, epoch)); removeErr != nil {
			return fmt.Errorf("remove retired HAP pairings: %w", removeErr)
		}
	}
	return nil
}

func pairingEpoch(name string) (uint64, bool) {
	if name == "hap" {
		return 0, true
	}
	value, ok := strings.CutPrefix(name, "hap-")
	if !ok {
		return 0, false
	}
	const epochBits = 64
	epoch, err := strconv.ParseUint(value, 10, epochBits)
	return epoch, err == nil && epoch != 0 && strconv.FormatUint(epoch, 10) == value
}
