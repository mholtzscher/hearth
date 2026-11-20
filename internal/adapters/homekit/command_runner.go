package homekit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"sync"
	"time"
)

const (
	hapCommunicationFailure = -70402
	hapBusy                 = -70403
	hapInvalidValue         = -70410

	writeBatchWindow = 25 * time.Millisecond
	maxWriteBatches  = 32
	maxBatchWrites   = 64

	groupHSV              = "hsv"
	groupColorTemperature = "color_temperature"
	groupTemperatureRange = "range"
)

type commandExecutor interface {
	ExecuteCommand(ctx context.Context, entityID, operation string, parameters any) error
}

type writeIntent struct {
	EntityID, Operation string
	Parameters          map[string]any
	Group, Field        string
	immediate           bool
	finish              func(failed bool)
}

type writeBatch struct {
	intents []writeIntent
	done    chan struct{}
}

type commandRunner struct {
	ctx     context.Context
	cancel  context.CancelFunc
	core    commandExecutor
	lookup  func(string) Entity
	logger  *slog.Logger
	mu      sync.Mutex
	wait    sync.WaitGroup
	pending map[uint64]*writeBatch
	tail    map[uint64]chan struct{}
	active  int
	closing bool
}

func newCommandRunner(
	ctx context.Context, core commandExecutor, lookup func(string) Entity, logger *slog.Logger,
) *commandRunner {
	ctx, cancel := context.WithCancel(ctx)
	return &commandRunner{
		ctx: ctx, cancel: cancel, core: core, lookup: lookup, logger: logger, pending: make(map[uint64]*writeBatch),
		tail: make(map[uint64]chan struct{}),
	}
}

func (r *commandRunner) submit(aid uint64, intent writeIntent) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || r.ctx.Err() != nil {
		return hapCommunicationFailure
	}
	if intent.Group != "" {
		if batch := r.pending[aid]; batch != nil {
			if len(batch.intents) == maxBatchWrites {
				return hapBusy
			}
			batch.intents = append(batch.intents, intent)
			return 0
		}
	}
	if r.active == maxWriteBatches {
		return hapBusy
	}
	batch := &writeBatch{intents: []writeIntent{intent}}
	if intent.Group != "" {
		r.pending[aid] = batch
	} else if !intent.immediate {
		// A later grouped write must not jump ahead of this separate batch.
		delete(r.pending, aid)
	}
	var previous <-chan struct{}
	if !intent.immediate {
		previous = r.tail[aid]
		batch.done = make(chan struct{})
		r.tail[aid] = batch.done
	}
	r.active++
	r.wait.Go(func() { r.runBatch(aid, batch, intent.Group != "", previous) })
	return 0
}

func (r *commandRunner) runBatch(aid uint64, batch *writeBatch, coalesce bool, previous <-chan struct{}) {
	defer func() {
		r.mu.Lock()
		if batch.done != nil {
			if r.tail[aid] == batch.done {
				delete(r.tail, aid)
			}
			close(batch.done)
		}
		r.active--
		r.mu.Unlock()
	}()
	if coalesce {
		timer := time.NewTimer(writeBatchWindow)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.ctx.Done():
		}
		r.mu.Lock()
		if r.pending[aid] == batch {
			delete(r.pending, aid)
		}
		r.mu.Unlock()
	}
	if previous != nil {
		select {
		case <-previous:
		case <-r.ctx.Done():
		}
	}
	failed := r.ctx.Err() != nil
	if !failed {
		failed = r.executeBatch(batch.intents)
	}
	for _, intent := range batch.intents {
		if intent.finish != nil {
			intent.finish(failed)
		}
	}
}

func (r *commandRunner) executeBatch(intents []writeIntent) bool {
	for _, intent := range collapseWrites(intents) {
		entity := r.lookup(intent.EntityID)
		parameters, ok := completedParameters(intent, entity.State)
		if !entity.reachable() || !ok {
			return true
		}
		ctx, cancel := context.WithTimeout(r.ctx, commandWaitBudget(entity, intent.Operation))
		err := r.core.ExecuteCommand(ctx, intent.EntityID, intent.Operation, parameters)
		cancel()
		if err != nil {
			r.logFailure(intent, err)
			return true
		}
	}
	return false
}

func (r *commandRunner) logFailure(intent writeIntent, err error) {
	if r.ctx.Err() != nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("event", "homekit.command_failed"), slog.String("entity_id", intent.EntityID),
		slog.String("operation", intent.Operation),
	}
	if failure, ok := errors.AsType[*CommandError](err); ok {
		attrs = append(attrs, slog.Int("http_status", failure.HTTPStatus))
		if failure.CommandID != "" {
			attrs = append(attrs, slog.String("command_id", failure.CommandID))
		}
	}
	r.logger.LogAttrs(r.ctx, slog.LevelWarn, "HomeKit Command did not confirm success", attrs...)
}

func (r *commandRunner) close() {
	r.mu.Lock()
	r.closing = true
	r.cancel()
	r.mu.Unlock()
	r.wait.Wait()
}

func collapseWrites(intents []writeIntent) []writeIntent {
	if len(intents) == 1 {
		return intents
	}
	groups := make(map[string]writeIntent)
	lastColor := ""
	for _, intent := range intents {
		if intent.Field != "" {
			parameters := make(map[string]any)
			maps.Copy(parameters, groups[intent.Group].Parameters)
			maps.Copy(parameters, intent.Parameters)
			intent.Parameters = parameters
		}
		groups[intent.Group] = intent
		if intent.Group == groupHSV || intent.Group == groupColorTemperature {
			lastColor = intent.Group
		}
	}
	if on, ok := groups["on"]; ok && on.Parameters["value"] == false {
		return []writeIntent{on}
	}
	switch lastColor {
	case groupHSV:
		delete(groups, groupColorTemperature)
	case groupColorTemperature:
		delete(groups, groupHSV)
	}
	order := []string{"on", "brightness", lastColor, groupTemperatureRange}
	out := make([]writeIntent, 0, len(groups))
	for _, group := range order {
		if intent, ok := groups[group]; ok {
			out = append(out, intent)
			delete(groups, group)
		}
	}
	for _, intent := range intents {
		if merged, ok := groups[intent.Group]; ok {
			out = append(out, merged)
			delete(groups, intent.Group)
		}
	}
	return out
}

func completedParameters(intent writeIntent, state *EntityState) (map[string]any, bool) {
	keys := map[string][]string{groupHSV: {"hue", "saturation"}, groupTemperatureRange: {"low", "high"}}[intent.Group]
	if len(keys) == 0 {
		if intent.Parameters == nil {
			return map[string]any{}, true
		}
		return intent.Parameters, true
	}
	parameters := maps.Clone(intent.Parameters)
	if parameters == nil {
		return nil, false
	}
	for _, key := range keys {
		if _, exists := parameters[key]; exists {
			continue
		}
		value, ok := pairedParameter(state, intent.Group, key)
		if !ok {
			return nil, false
		}
		parameters[key] = value
	}
	return parameters, true
}

func pairedParameter(state *EntityState, group, field string) (int, bool) {
	if state == nil {
		return 0, false
	}
	if group == groupTemperatureRange {
		field = "target_temperature_" + field
	}
	var fields map[string]json.RawMessage
	var value *int
	if json.Unmarshal(state.Value, &fields) != nil || json.Unmarshal(fields[field], &value) != nil || value == nil {
		return 0, false
	}
	return *value, true
}

func commandWaitBudget(entity Entity, operation string) time.Duration {
	const responseMargin = 5 * time.Second
	const ordinaryDeadline = 10 * time.Second
	const lockDeadline = 30 * time.Second
	const positionDeadline = time.Minute
	switch {
	case entity.Type == "hearth.lock/v1":
		return lockDeadline + responseMargin
	case entity.Type == "hearth.cover/v1" && operation == "set":
		return positionDeadline + responseMargin
	default:
		return ordinaryDeadline + responseMargin
	}
}
