package esphome

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"time"

	contractenumsettingv1 "github.com/mholtzscher/hearth/entitytypes/enumsettingv1"
	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkenumsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/enumsettingv1"
	sdknumericsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsettingv1"
)

func (planner *entityPlanner) planSetting(
	name, identity string, upstream upstreamRef, entity nativeapi.EntityInfo,
) error {
	route, err := settingRoute(upstream, entity)
	if err != nil {
		if _, invalid := errors.AsType[*adapter.ValidationError](err); !invalid {
			return err
		}
		planner.skipCapability(entity, skipCapabilityUnsupported)
		return nil
	}
	return planner.plan(name, identity, route)
}

func settingRoute(upstream upstreamRef, entity nativeapi.EntityInfo) (entityRoute, error) {
	if entity.Type == nativeapi.EntityNumber {
		return numberSettingRoute(upstream, entity)
	}
	support := sdkenumsettingv1.Support{
		State:      sdkenumsettingv1.StateSupport{Choices: slices.Clone(entity.Options)},
		Operations: sdkenumsettingv1.OperationSupport{Set: sdkenumsettingv1.SetSupport{}},
	}
	codecs, err := contractenumsettingv1.Compile()
	if err != nil {
		return entityRoute{}, err
	}
	if _, err = codecs.Support.Encode(support); err != nil {
		return entityRoute{}, &adapter.ValidationError{Err: err}
	}
	return entityRoute{Upstream: upstream, Kind: routeSelect, Select: support}, nil
}

func numberSettingRoute(upstream upstreamRef, entity nativeapi.EntityInfo) (entityRoute, error) {
	if entity.Number == nil || !finiteNumber(entity.Number.Minimum) || !finiteNumber(entity.Number.Maximum) ||
		entity.Number.Minimum > entity.Number.Maximum {
		return entityRoute{}, &adapter.ValidationError{Err: errors.New("invalid native number bounds")}
	}
	support := sdknumericsettingv1.Support{
		State: sdknumericsettingv1.StateSupport{
			Minimum: entity.Number.Minimum, Maximum: entity.Number.Maximum, Choices: []string{},
		},
		Operations: sdknumericsettingv1.OperationSupport{Set: sdknumericsettingv1.SetSupport{}},
	}
	if entity.Unit != "" {
		support.State.Unit = &entity.Unit
	}
	codecs, err := contractnumericsettingv1.Compile()
	if err != nil {
		return entityRoute{}, err
	}
	if _, err = codecs.Support.Encode(support); err != nil {
		return entityRoute{}, &adapter.ValidationError{Err: err}
	}
	if err = contractnumericsettingv1.ValidateSupport(support); err != nil {
		return entityRoute{}, &adapter.ValidationError{Err: err}
	}
	return entityRoute{Upstream: upstream, Kind: routeNumber, Number: support}, nil
}

func settingObservation(
	route entityRoute, entityID string, update nativeapi.StateUpdate, receivedAt time.Time,
) (adapter.Observation, error) {
	if route.Kind == routeNumber {
		return sdknumericsettingv1.NewObservation(sdknumericsettingv1.ObservationInput{
			EntityID: entityID, Support: route.Number,
			State: sdknumericsettingv1.State{Mode: "value", Value: &update.Number}, AdapterReceivedAt: receivedAt,
		})
	}
	return sdkenumsettingv1.NewObservation(sdkenumsettingv1.ObservationInput{
		EntityID: entityID, Support: route.Select, State: sdkenumsettingv1.State(update.Text),
		AdapterReceivedAt: receivedAt,
	})
}

func finiteNumber(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// wireNumber preserves the shortest-decimal float32 interpretation used by the native decoder.
func wireNumber(value float64) (float64, error) {
	if !finiteNumber(value) || math.Abs(value) > math.MaxFloat32 {
		return 0, errors.New("number is outside native precision")
	}
	normalized, err := strconv.ParseFloat(strconv.FormatFloat(float64(float32(value)), 'g', -1, 32), 64)
	if err != nil || normalized != value {
		return 0, errors.New("number is outside native precision")
	}
	return value, nil
}

func prepareSettingSet(route boundRoute, parameters json.RawMessage) (json.RawMessage, nativeapi.Command, error) {
	if route.Kind == routeSelect {
		codecs, err := contractenumsettingv1.Compile()
		if err != nil {
			return nil, nil, err
		}
		decoded, _, err := codecs.SetParameters.Decode(parameters)
		if err != nil {
			return nil, nil, err
		}
		if err = contractenumsettingv1.ValidateSetParameters(
			route.Select,
			route.Select.Operations.Set,
			decoded,
		); err != nil {
			return nil, nil, err
		}
		target, err := json.Marshal(decoded.Value)
		return target, nativeapi.SelectCommand{
			Key: route.Upstream.Key, DeviceID: route.Upstream.DeviceID, Value: decoded.Value,
		}, err
	}
	codecs, err := contractnumericsettingv1.Compile()
	if err != nil {
		return nil, nil, err
	}
	decoded, normalized, err := codecs.SetParameters.Decode(parameters)
	if err != nil {
		return nil, nil, err
	}
	if err = contractnumericsettingv1.ValidateSetParameters(
		route.Number,
		route.Number.Operations.Set,
		decoded,
	); err != nil {
		return nil, nil, err
	}
	if decoded.Mode != "value" || decoded.Value == nil {
		return nil, nil, errors.New("ESPHome number requires value mode")
	}
	value, err := wireNumber(*decoded.Value)
	if err != nil {
		return nil, nil, err
	}
	return normalized, nativeapi.NumberCommand{
		Key: route.Upstream.Key, DeviceID: route.Upstream.DeviceID, Value: value,
	}, nil
}
