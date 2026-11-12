package levoit

import (
	"context"
	"strconv"

	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/enumsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter/numericsensorv1"
	"github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

// An entityPlan keeps each capability's descriptor, report and typed route together.
type entityPlan struct {
	descriptor adapter.EntityDescriptor
	observe    func(string, status) (adapter.Observation, bool, error)
	handler    func(string, *deviceRuntime) (adapter.CommandHandler, error)
}

func (a *Adapter) entities(d device) ([]entityPlan, error) {
	var plans []entityPlan
	for _, definition := range []struct {
		key, name string
		read      func(status) *bool
		set       func(context.Context, device, bool) error
		predict   func(bool) status
	}{
		{"power", "Power", func(s status) *bool { return s.Power }, a.client.setPower,
			func(value bool) status { return status{Power: &value} }},
		{"display", "Display", func(s status) *bool { return s.Display }, a.client.setDisplay,
			func(value bool) status { return status{Display: &value} }},
		{"child-lock", "Child lock", func(s status) *bool { return s.ChildLock }, a.client.setChildLock,
			func(value bool) status { return status{ChildLock: &value} }},
	} {
		plan, err := a.booleanEntity(
			d,
			definition.key,
			definition.name,
			definition.read,
			definition.set,
			definition.predict,
		)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	mode, err := a.choiceEntity(d, "mode", "Mode", []string{modeManual, modeAuto, modeSleep},
		func(s status) *string { return s.Mode }, a.client.setMode, predictMode)
	if err != nil {
		return nil, err
	}
	level, err := a.choiceEntity(d, "fan-level", "Manual fan level", []string{"1", "2", "3", "4"},
		func(s status) *string {
			if s.ManualLevel == nil {
				return nil
			}
			value := strconv.Itoa(*s.ManualLevel)
			return &value
		}, func(ctx context.Context, d device, value string) error {
			level, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return parseErr
			}
			return a.client.setLevel(ctx, d, level)
		}, predictLevel)
	if err != nil {
		return nil, err
	}
	plans = append(plans, mode, level)
	for _, definition := range []struct {
		key, name, unit string
		maximum         float64
		read            func(status) *float64
	}{
		{"pm25", "PM2.5", "ug/m3", maximumPM25, func(s status) *float64 { return s.PM25 }},
		{"filter-life", "Filter life", "%", maximumPercent, func(s status) *float64 { return s.FilterLife }},
	} {
		plan, planErr := sensorEntity(
			d,
			definition.key,
			definition.name,
			definition.unit,
			definition.maximum,
			definition.read,
		)
		if planErr != nil {
			return nil, planErr
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func metadata(d device, key, name string) adapter.EntityMetadata {
	return adapter.EntityMetadata{Key: key, ExternalID: d.CID + "/" + key, Name: name}
}

func (a *Adapter) booleanEntity(d device, key, name string, read func(status) *bool,
	set func(context.Context, device, bool) error, predict func(bool) status) (entityPlan, error) {
	support := powerv1.Support{}
	descriptor, err := powerv1.NewEntityDescriptor(metadata(d, key, name), support)
	if err != nil {
		return entityPlan{}, err
	}
	return entityPlan{descriptor: descriptor,
		observe: func(id string, s status) (adapter.Observation, bool, error) {
			value := read(s)
			if value == nil {
				return adapter.Observation{}, false, nil
			}
			observation, observationErr := powerv1.NewObservation(powerv1.ObservationInput{
				EntityID: id, Support: support, State: powerv1.State(*value), AdapterReceivedAt: s.ReceivedAt,
			})
			return observation, true, observationErr
		},
		handler: func(id string, runtime *deviceRuntime) (adapter.CommandHandler, error) {
			return powerv1.NewCommandHandler(id, support, powerv1.Handlers{Set: func(ctx context.Context,
				command powerv1.SetCommand, responder adapter.Responder) error {
				return a.execute(
					ctx,
					runtime,
					key,
					responder,
					func(d device) error { return set(ctx, d, command.Parameters.Value) },
					predict(command.Parameters.Value),
				)
			}})
		},
	}, nil
}

func (a *Adapter) choiceEntity(d device, key, name string, choices []string, read func(status) *string,
	set func(context.Context, device, string) error, predict func(string) (status, error)) (entityPlan, error) {
	support := enumsettingv1.Support{State: enumsettingv1.StateSupport{Choices: choices}}
	descriptor, err := enumsettingv1.NewEntityDescriptor(metadata(d, key, name), support)
	if err != nil {
		return entityPlan{}, err
	}
	return entityPlan{descriptor: descriptor,
		observe: func(id string, s status) (adapter.Observation, bool, error) {
			value := read(s)
			if value == nil {
				return adapter.Observation{}, false, nil
			}
			observation, observationErr := enumsettingv1.NewObservation(enumsettingv1.ObservationInput{
				EntityID: id, Support: support, State: enumsettingv1.State(*value), AdapterReceivedAt: s.ReceivedAt,
			})
			return observation, true, observationErr
		},
		handler: func(id string, runtime *deviceRuntime) (adapter.CommandHandler, error) {
			return enumsettingv1.NewCommandHandler(id, support, enumsettingv1.Handlers{Set: func(ctx context.Context,
				command enumsettingv1.SetCommand, responder adapter.Responder) error {
				requested, predictErr := predict(command.Parameters.Value)
				if predictErr != nil {
					return predictErr
				}
				return a.execute(
					ctx,
					runtime,
					key,
					responder,
					func(d device) error { return set(ctx, d, command.Parameters.Value) },
					requested,
				)
			}})
		},
	}, nil
}

func predictMode(value string) (status, error) {
	on := true
	report := status{Mode: &value, Power: &on}
	if value == modeManual {
		level := 1
		report.ManualLevel = &level
	}
	return report, nil
}

func predictLevel(value string) (status, error) {
	level, err := strconv.Atoi(value)
	if err != nil {
		return status{}, err
	}
	on, mode := true, modeManual
	return status{ManualLevel: &level, Mode: &mode, Power: &on}, nil
}

func sensorEntity(d device, key, name, unit string, maximum float64,
	read func(status) *float64) (entityPlan, error) {
	support := numericsensorv1.Support{State: numericsensorv1.StateSupport{Minimum: 0, Maximum: maximum, Unit: unit}}
	descriptor, err := numericsensorv1.NewEntityDescriptor(metadata(d, key, name), support)
	if err != nil {
		return entityPlan{}, err
	}
	return entityPlan{descriptor: descriptor,
		observe: func(id string, s status) (adapter.Observation, bool, error) {
			value := read(s)
			if value == nil {
				return adapter.Observation{}, false, nil
			}
			observation, observationErr := numericsensorv1.NewObservation(numericsensorv1.ObservationInput{
				EntityID: id, Support: support, State: numericsensorv1.State(*value), AdapterReceivedAt: s.ReceivedAt,
			})
			return observation, true, observationErr
		},
	}, nil
}
