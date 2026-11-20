package homekit

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

var (
	errReloadFailed = errors.New("HomeKit reload failed after persistence")
	errBridgeBusy   = errors.New("HomeKit bridge busy")
)

type functionRuntime interface {
	inventorySnapshot() Inventory
	exposureSnapshot() storeFile
	pairingStatus() (int, SetupIdentity, error)
	changeExposure(context.Context, Exposure, bool) (Exposure, bool, error)
	resetPairing(context.Context) error
}

const (
	functionArrayLimit     = 512
	functionReadTimeout    = 10000
	functionWriteTimeout   = 30000
	reasonBridgeBusy       = "bridge_busy"
	reasonInvalidArguments = "invalid_arguments"
	reasonCoreUnreachable  = "core_unreachable"
	reasonDeviceNotFound   = "device_not_found"
	reasonKindIncompatible = "kind_incompatible"
	reasonRolesUnresolved  = "roles_unresolved"
)

func bridgeFunctions(runtime functionRuntime) []adapter.Function {
	return []adapter.Function{
		bridgeFunction(
			"list_accessories",
			"List exposed accessories and candidate Devices. Truncated indicates omitted entries.",
			emptyInputSchema,
			listOutputSchema,
			functionReadTimeout,
			func(ctx context.Context, _ json.RawMessage) adapter.FunctionResult {
				return listAccessories(ctx, runtime)
			},
		),
		bridgeFunction(
			"expose_device",
			"Expose or replace a Device as a HomeKit accessory.",
			exposeInputSchema,
			accessorySchema,
			functionWriteTimeout,
			func(ctx context.Context, input json.RawMessage) adapter.FunctionResult {
				return exposeDevice(ctx, runtime, input)
			},
		),
		bridgeFunction(
			"unexpose_device",
			"Remove a Device from HomeKit while retaining its identifiers.",
			unexposeInputSchema,
			unexposeOutputSchema,
			functionWriteTimeout,
			func(ctx context.Context, input json.RawMessage) adapter.FunctionResult {
				return unexposeDevice(ctx, runtime, input)
			},
		),
		bridgeFunction(
			"pairing_info",
			"Get pairing status and, only while unpaired, the setup credentials. Function history retains credentials for 30 days.",
			emptyInputSchema,
			pairingOutputSchema,
			functionReadTimeout,
			func(_ context.Context, _ json.RawMessage) adapter.FunctionResult {
				return pairingInfo(runtime)
			},
		),
		bridgeFunction(
			"reset_pairing",
			"Reset every HomeKit pairing: every iPhone loses the bridge and Home must be set up again. New setup credentials are retained in Function history for 30 days.",
			resetInputSchema,
			pairingOutputSchema,
			functionWriteTimeout,
			func(ctx context.Context, _ json.RawMessage) adapter.FunctionResult {
				if err := runtime.resetPairing(ctx); err != nil {
					return functionMutationFailure(err)
				}
				return pairingInfo(runtime)
			},
		),
	}
}

func bridgeFunction(
	key, description, input, output string,
	timeout int,
	handler func(context.Context, json.RawMessage) adapter.FunctionResult,
) adapter.Function {
	inputSchema, err := functionschema.Compile(schemaBytes(input))
	if err != nil {
		panic("invalid HomeKit function input schema")
	}
	return adapter.Function{
		Descriptor: adapter.FunctionDescriptor{
			Key:          key,
			Description:  description,
			InputSchema:  schemaBytes(input),
			OutputSchema: schemaBytes(output),
			TimeoutMS:    timeout,
		},
		Handler: func(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
			if inputSchema.Validate(request.Arguments) != nil {
				return adapter.FunctionNotExecuted{ReasonCode: reasonInvalidArguments}
			}
			return handler(ctx, request.Arguments)
		},
	}
}

type accessoryResult struct {
	DeviceID string           `json:"device_id"`
	Name     string           `json:"name"`
	Kind     AccessoryKind    `json:"kind"`
	AID      uint64           `json:"aid"`
	Status   string           `json:"status"`
	Entities []RoleAssignment `json:"entities"`
}

type namedEntity struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

type unresolvedResult struct {
	Role       Role          `json:"role"`
	Reason     string        `json:"reason"`
	Candidates []namedEntity `json:"candidates"`
}

type candidateResult struct {
	DeviceID     string             `json:"device_id"`
	Name         string             `json:"name"`
	DeviceKind   string             `json:"device_kind"`
	DefaultKind  AccessoryKind      `json:"default_kind,omitempty"`
	AllowedKinds []AccessoryKind    `json:"allowed_kinds"`
	Proposed     []RoleAssignment   `json:"proposed"`
	Unresolved   []unresolvedResult `json:"unresolved"`
	Unmapped     []namedEntity      `json:"unmapped"`
}

type listResult struct {
	Paired         bool              `json:"paired"`
	AccessoryCount int               `json:"accessory_count"`
	AccessoryLimit int               `json:"accessory_limit"`
	Accessories    []accessoryResult `json:"accessories"`
	Candidates     []candidateResult `json:"candidates"`
	Truncated      bool              `json:"truncated"`
}

func listAccessories(_ context.Context, runtime functionRuntime) adapter.FunctionResult {
	inventory := runtime.inventorySnapshot()
	if inventory.ReadAt.IsZero() {
		return adapter.FunctionNotExecuted{ReasonCode: reasonCoreUnreachable}
	}
	count, _, err := runtime.pairingStatus()
	if err != nil {
		return adapter.FunctionNotExecuted{ReasonCode: reasonBridgeBusy}
	}
	store := runtime.exposureSnapshot()
	result := listResult{
		Paired:         count > 0,
		AccessoryCount: len(store.Exposures),
		AccessoryLimit: accessoryLimit,
		Accessories:    []accessoryResult{},
		Candidates:     []candidateResult{},
	}
	ids := sortedKeys(store.Exposures)
	for _, id := range ids {
		item := describeAccessory(store.Exposures[id], inventory)
		if len(item.Entities) > functionArrayLimit {
			item.Entities = item.Entities[:functionArrayLimit]
			result.Truncated = true
		}
		result.Accessories = append(result.Accessories, item)
		if len(result.Accessories) > functionArrayLimit || !listFits(result) {
			result.Accessories = result.Accessories[:len(result.Accessories)-1]
			result.Truncated = true
		}
	}
	entities := inventoryEntities(inventory)
	for _, id := range sortedKeys(inventory.Devices) {
		if _, exposed := store.Exposures[id]; exposed {
			continue
		}
		device := inventory.Devices[id]
		item, ok := describeCandidate(device, inventory, entities, &result.Truncated)
		if !ok {
			continue
		}
		result.Candidates = append(result.Candidates, item)
		if len(result.Candidates) > functionArrayLimit || !listFits(result) {
			result.Candidates = result.Candidates[:len(result.Candidates)-1]
			result.Truncated = true
		}
	}
	return functionSuccess(result)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func inventoryEntities(inventory Inventory) []Entity {
	entities := make([]Entity, 0, len(inventory.Entities))
	for _, id := range sortedKeys(inventory.Entities) {
		entities = append(entities, inventory.Entities[id])
	}
	return entities
}

func describeAccessory(exposure Exposure, inventory Inventory) accessoryResult {
	device, exists := inventory.Devices[exposure.DeviceID]
	name := exposure.Name
	if name == "" {
		name = device.Name
	}
	item := accessoryResult{
		DeviceID: exposure.DeviceID,
		Name:     name,
		Kind:     exposure.Kind,
		AID:      exposure.AID,
		Status:   "reachable",
		Entities: slices.Clone(exposure.Entities),
	}
	if item.Entities == nil {
		item.Entities = []RoleAssignment{}
	}
	if !exists {
		item.Status = "missing"
		return item
	}
	reachable := 0
	for _, assignment := range exposure.Entities {
		entity, ok := inventory.Entities[assignment.EntityID]
		if !ok || entity.DeviceID != exposure.DeviceID {
			item.Status = "missing"
			return item
		}
		if entity.reachable() {
			reachable++
		}
	}
	if reachable == 0 {
		item.Status = "unreachable"
	} else if reachable < len(exposure.Entities) {
		item.Status = "partially_unreachable"
	}
	return item
}

func describeCandidate(device Device, inventory Inventory, entities []Entity, truncated *bool) (candidateResult, bool) {
	kind := defaultAccessoryKind(device.Kind)
	power := hasDeviceEntityType(inventory, device.ID, powerv1.TypeID)
	if kind == "" && !power {
		return candidateResult{}, false
	}
	item := candidateResult{
		DeviceID:     device.ID,
		Name:         device.Name,
		DeviceKind:   device.Kind,
		DefaultKind:  kind,
		AllowedKinds: []AccessoryKind{},
		Proposed:     []RoleAssignment{},
		Unresolved:   []unresolvedResult{},
		Unmapped:     []namedEntity{},
	}
	if kind != "" {
		item.AllowedKinds = append(item.AllowedKinds, kind)
	}
	if power {
		for _, extra := range []AccessoryKind{KindSwitch, KindOutlet} {
			if !slices.Contains(item.AllowedKinds, extra) {
				item.AllowedKinds = append(item.AllowedKinds, extra)
			}
		}
	}
	if kind == "" {
		return item, true
	}
	proposal := Propose(device, entities, kind)
	item.Proposed = bounded(proposal.Assigned, truncated)
	for _, unresolved := range bounded(proposal.Unresolved, truncated) {
		row := unresolvedResult{Role: unresolved.Role, Reason: unresolved.Reason, Candidates: []namedEntity{}}
		for _, id := range bounded(unresolved.Candidates, truncated) {
			row.Candidates = append(row.Candidates, namedEntity{EntityID: id, Name: inventory.Entities[id].Name})
		}
		item.Unresolved = append(item.Unresolved, row)
	}
	for _, id := range bounded(proposal.Unmapped, truncated) {
		item.Unmapped = append(item.Unmapped, namedEntity{EntityID: id, Name: inventory.Entities[id].Name})
	}
	return item, true
}

func bounded[T any](values []T, truncated *bool) []T {
	if len(values) > functionArrayLimit {
		*truncated = true
		values = values[:functionArrayLimit]
	}
	if values == nil {
		return []T{}
	}
	return values
}

func listFits(result listResult) bool {
	// Leave room for the longer spelling of false when toggling truncated.
	result.Truncated = false
	data, err := json.Marshal(result)
	return err == nil && len(data) <= functionschema.MaxValueBytes
}

func exposeDevice(ctx context.Context, runtime functionRuntime, input json.RawMessage) adapter.FunctionResult {
	var request struct {
		DeviceID string            `json:"device_id"`
		Kind     AccessoryKind     `json:"kind"`
		Name     string            `json:"name"`
		Entities *[]RoleAssignment `json:"entities"`
	}
	if json.Unmarshal(input, &request) != nil {
		return adapter.FunctionNotExecuted{ReasonCode: reasonInvalidArguments}
	}
	inventory := runtime.inventorySnapshot()
	if inventory.ReadAt.IsZero() {
		return adapter.FunctionNotExecuted{ReasonCode: reasonCoreUnreachable}
	}
	device, exists := inventory.Devices[request.DeviceID]
	if !exists {
		return adapter.FunctionNotExecuted{ReasonCode: reasonDeviceNotFound}
	}
	desired := Exposure{DeviceID: request.DeviceID, Kind: request.Kind, Name: request.Name}
	if desired.Kind == "" {
		desired.Kind = defaultAccessoryKind(device.Kind)
	}
	if !compatibleKind(device, inventory, desired.Kind) {
		return adapter.FunctionNotExecuted{ReasonCode: reasonKindIncompatible}
	}
	if request.Entities == nil {
		proposal := Propose(device, inventoryEntities(inventory), desired.Kind)
		if len(proposal.Unresolved) > 0 {
			return adapter.FunctionNotExecuted{ReasonCode: reasonRolesUnresolved}
		}
		desired.Entities = proposal.Assigned
	} else {
		desired.Entities = *request.Entities
	}
	if err := ValidateExposure(desired, inventory); err != nil {
		return functionMutationFailure(err)
	}
	// Check before persistence: an inferred assignment set may be much larger
	// than the request, but the SDK cannot deliver an oversized result.
	desired.AID = math.MaxUint64
	preview, err := json.Marshal(describeAccessory(desired, inventory))
	desired.AID = 0
	if err != nil || len(desired.Entities) > functionArrayLimit || len(preview) > functionschema.MaxValueBytes {
		return adapter.FunctionNotExecuted{ReasonCode: "result_too_large"}
	}
	saved, _, err := runtime.changeExposure(ctx, desired, false)
	if err != nil {
		return functionMutationFailure(err)
	}
	return functionSuccess(describeAccessory(saved, inventory))
}

func unexposeDevice(ctx context.Context, runtime functionRuntime, input json.RawMessage) adapter.FunctionResult {
	var request struct {
		DeviceID string `json:"device_id"`
	}
	if json.Unmarshal(input, &request) != nil {
		return adapter.FunctionNotExecuted{ReasonCode: reasonInvalidArguments}
	}
	_, removed, err := runtime.changeExposure(ctx, Exposure{DeviceID: request.DeviceID}, true)
	if err != nil {
		return functionMutationFailure(err)
	}
	status := "not_exposed"
	if removed {
		status = "unexposed"
	}
	return functionSuccess(struct {
		DeviceID string `json:"device_id"`
		Status   string `json:"status"`
	}{request.DeviceID, status})
}

type pairingResult struct {
	Paired          bool   `json:"paired"`
	ControllerCount int    `json:"controller_count,omitempty"`
	SetupCode       string `json:"setup_code,omitempty"`
	SetupURI        string `json:"setup_uri,omitempty"`
}

func pairingInfo(runtime functionRuntime) adapter.FunctionResult {
	count, setup, err := runtime.pairingStatus()
	if err != nil {
		return adapter.FunctionNotExecuted{ReasonCode: reasonBridgeBusy}
	}
	if count > 0 {
		return functionSuccess(pairingResult{Paired: true, ControllerCount: count})
	}
	return unpairedResult(setup)
}

func unpairedResult(setup SetupIdentity) adapter.FunctionResult {
	uri, err := setup.setupURI()
	if err != nil {
		return adapter.FunctionFailed{ReasonCode: "invalid_response"}
	}
	code := strings.Join([]string{setup.Code[:3], setup.Code[3:5], setup.Code[5:]}, "-")
	return functionSuccess(pairingResult{SetupCode: code, SetupURI: uri})
}

func functionMutationFailure(err error) adapter.FunctionResult {
	switch {
	case errors.Is(err, errStoreDurabilityUncertain):
		return adapter.FunctionUnknown{ReasonCode: "store_write_failed"}
	case errors.Is(err, errReloadFailed):
		return adapter.FunctionFailed{ReasonCode: "reload_failed"}
	case errors.Is(err, errBridgeBusy):
		return adapter.FunctionNotExecuted{ReasonCode: reasonBridgeBusy}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return adapter.FunctionUnknown{ReasonCode: "bridge_mutation_interrupted"}
	}
	if plan, ok := errors.AsType[*PlanError](err); ok {
		switch plan.Reason {
		case reasonDeviceNotFound,
			reasonKindIncompatible,
			reasonRolesUnresolved,
			"entity_not_on_device",
			"entity_type_mismatch",
			"duplicate_role",
			"capacity_reached",
			reasonCoreUnreachable:
			return adapter.FunctionNotExecuted{ReasonCode: plan.Reason}
		}
	}
	return adapter.FunctionNotExecuted{ReasonCode: "store_write_failed"}
}

func functionSuccess(value any) adapter.FunctionResult {
	data, err := json.Marshal(value)
	if err != nil || len(data) > functionschema.MaxValueBytes {
		return adapter.FunctionFailed{ReasonCode: "invalid_response"}
	}
	return adapter.FunctionSucceeded{Result: data}
}
