package devices_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gocognit // The explicit inventory is the independent schema-contract oracle.
func TestBuiltinStateFieldInventory(t *testing.T) {
	t.Parallel()
	//nolint:exhaustive // Stateless types are asserted separately below.
	inventory := map[devices.EntityTypeID]map[string]string{
		devices.EntityTypeTemperatureV1: {"": "mCel"}, devices.EntityTypeRelativehumidityV1: {"": "%"},
		devices.EntityTypePressureV1: {"": "hPa"}, devices.EntityTypeSpeedV1: {"": "m/s"},
		devices.EntityTypeNumericsensorV1: {"": "support"}, devices.EntityTypeBrightnessV1: {"": "%"},
		devices.EntityTypeColorhsV1:        {"/active": "boolean", "/hue": "deg", "/saturation": "%"},
		devices.EntityTypeColortempV1:      {"/active": "boolean", "/value": "/MK"},
		devices.EntityTypeColorxyV1:        {"/active": "boolean", "/x": "10*-4", "/y": "10*-4"},
		devices.EntityTypeNumericsettingV1: {"/mode": "string", "/choice": "string", "/value": "support"},
		devices.EntityTypePowerV1:          {"": "boolean"}, devices.EntityTypeBinarysensorV1: {"": "boolean"},
		devices.EntityTypeColormodeV1: {"": "string"}, devices.EntityTypeEnumsettingV1: {"": "string"},
	}
	for id, expected := range inventory {
		fields := devices.BuiltinStateFields(id)
		if len(fields) != len(expected) {
			t.Fatalf("%s fields = %v", id, fields)
		}
		for path, kind := range expected {
			field, ok := devices.BuiltinStateField(id, path)
			if !ok {
				t.Fatalf("%s %s absent", id, path)
			}
			presence := devices.FieldAlways
			if id == devices.EntityTypeNumericsettingV1 && path != "/mode" {
				presence = devices.FieldSometimes
			}
			if field.Path != path || field.Presence != presence {
				t.Fatalf("%s %s = %+v", id, path, field)
			}
			switch value := field.Value.(type) {
			case devices.NumericField:
				expectedType := devices.NumericNumber
				if id == devices.EntityTypeTemperatureV1 || id == devices.EntityTypeBrightnessV1 ||
					id == devices.EntityTypeColorhsV1 ||
					id == devices.EntityTypeColortempV1 ||
					id == devices.EntityTypeColorxyV1 {
					expectedType = devices.NumericInteger
				}
				if value.ValueType != expectedType {
					t.Fatalf("%s %s value type = %s", id, path, value.ValueType)
				}
				switch unit := value.Unit.(type) {
				case devices.FixedUnit:
					if unit.Code != kind {
						t.Fatalf("%s %s unit = %v", id, path, unit)
					}
				case devices.SupportUnit:
					if kind != "support" || unit.Reference.Path != "/state/unit" ||
						unit.Optional != (id == devices.EntityTypeNumericsettingV1) {
						t.Fatalf("%s %s unit = %v", id, path, unit)
					}
				default:
					t.Fatalf("%s %s missing unit", id, path)
				}
			case devices.BooleanField:
				if kind != "boolean" {
					t.Fatal(field)
				}
			case devices.StringField:
				if kind != "string" {
					t.Fatal(field)
				}
			default:
				t.Fatalf("unexpected field = %v", field)
			}
		}
	}
}

func TestStateFieldLookupRejectsStatelessTypesAndUnknownPaths(t *testing.T) {
	t.Parallel()
	for _, id := range []devices.EntityTypeID{devices.EntityTypeEnumactionV1, devices.EntityTypeEnumeventV1, "unknown/v1"} {
		if fields := devices.BuiltinStateFields(id); len(fields) != 0 {
			t.Fatalf("%s fields = %v", id, fields)
		}
	}
	if _, ok := devices.BuiltinStateField(devices.EntityTypeTemperatureV1, "/unknown"); ok {
		t.Fatal("unknown path exists")
	}
}

func TestStateFieldLookupsReturnIsolatedCopies(t *testing.T) {
	t.Parallel()
	field, _ := devices.BuiltinStateField(devices.EntityTypeColormodeV1, "")
	field.Value.(devices.StringField).Enum[1] = "changed"
	fields := devices.BuiltinStateFields(devices.EntityTypeColormodeV1)
	fields[0].Value.(devices.StringField).Enum[0] = "changed"
	fields[0].Path = "/changed"
	enum, _ := devices.BuiltinStateField(devices.EntityTypeColormodeV1, "")
	if !reflect.DeepEqual(enum.Value.(devices.StringField).Enum, []string{"xy", "hs", "color_temp"}) ||
		enum.Path != "" {
		t.Fatal("string metadata aliased")
	}
}

func TestResolveQuantitySupportValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		id                  devices.EntityTypeID
		path, support, unit string
		invalid             bool
	}{
		{name: "fixed", id: devices.EntityTypeTemperatureV1, support: `{}`, unit: "mCel"},
		{name: "opaque", id: devices.EntityTypeNumericsensorV1, support: `{"state":{"unit":"lqi"}}`, unit: "lqi"},
		{
			name:    "optional absent",
			id:      devices.EntityTypeNumericsettingV1,
			path:    "/value",
			support: `{"state":{}}`,
			unit:    "1",
		},
		{
			name:    "optional present",
			id:      devices.EntityTypeNumericsettingV1,
			path:    "/value",
			support: `{"state":{"unit":"%"}}`,
			unit:    "%",
		},
		{name: "required absent", id: devices.EntityTypeNumericsensorV1, support: `{"state":{}}`, invalid: true},
		{name: "empty", id: devices.EntityTypeNumericsensorV1, support: `{"state":{"unit":""}}`, invalid: true},
		{
			name:    "wrong type",
			id:      devices.EntityTypeNumericsettingV1,
			path:    "/value",
			support: `{"state":{"unit":1}}`,
			invalid: true,
		},
		{
			name:    "null unit",
			id:      devices.EntityTypeNumericsettingV1,
			path:    "/value",
			support: `{"state":{"unit":null}}`,
			invalid: true,
		},
		{name: "brightness percent", id: devices.EntityTypeBrightnessV1, support: `{"state":{}}`, unit: "%"},
		{name: "invalid support", id: devices.EntityTypeNumericsensorV1, support: `[]`, invalid: true},
		{
			name:    "trailing support",
			id:      devices.EntityTypeNumericsensorV1,
			support: `{"state":{"unit":"V"}} {}`,
			invalid: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			field, _ := devices.BuiltinStateField(test.id, test.path)
			quantity, err := devices.ResolveQuantity(field.Value.(devices.NumericField), json.RawMessage(test.support))
			if (err != nil) != test.invalid {
				t.Fatalf("quantity=%+v error=%v", quantity, err)
			}
			if test.invalid {
				return
			}
			if quantity.Unit != test.unit {
				t.Fatalf("quantity=%+v", quantity)
			}
		})
	}
}

func TestInterpretationChangesUseOnlyReferencedPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		id            devices.EntityTypeID
		before, after string
		changed       bool
	}{
		{
			"same unit",
			devices.EntityTypeNumericsensorV1,
			`{"state":{"unit":"lqi","maximum":100}}`,
			`{"state":{"unit":"lqi","maximum":200}}`,
			false,
		},
		{"unit change", devices.EntityTypeNumericsensorV1, `{"state":{"unit":"V"}}`, `{"state":{"unit":"mV"}}`, true},
		{
			"optional unchanged",
			devices.EntityTypeNumericsettingV1,
			`{"state":{"choices":["a"]}}`,
			`{"state":{"choices":["b"]}}`,
			false,
		},
		{"optional added", devices.EntityTypeNumericsettingV1, `{"state":{}}`, `{"state":{"unit":"1"}}`, true},
		{"optional removed", devices.EntityTypeNumericsettingV1, `{"state":{"unit":"1"}}`, `{"state":{}}`, true},
		{"kind absent", devices.EntityTypeNumericsensorV1, `{"state":{"unit":"%"}}`, `{"state":{"unit":"%"}}`, false},
		{
			"kind added",
			devices.EntityTypeNumericsensorV1,
			`{"state":{"unit":"%"}}`,
			`{"state":{"unit":"%","quantity_kind":"brightness"}}`,
			false,
		},
		{
			"kind same",
			devices.EntityTypeNumericsensorV1,
			`{"state":{"unit":"%","quantity_kind":"brightness"}}`,
			`{"state":{"unit":"%","quantity_kind":"brightness"}}`,
			false,
		},
		{
			"kind removed",
			devices.EntityTypeNumericsensorV1,
			`{"state":{"unit":"%","quantity_kind":"brightness"}}`,
			`{"state":{"unit":"%"}}`,
			true,
		},
		{
			"kind changed",
			devices.EntityTypeNumericsensorV1,
			`{"state":{"unit":"%","quantity_kind":"brightness"}}`,
			`{"state":{"unit":"%","quantity_kind":"battery_level"}}`,
			true,
		},
		{"fixed support mutable", devices.EntityTypeColorhsV1, `{"state":{"hue":10}}`, `{"state":{"hue":20}}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := devices.ValidateInterpretationUnchanged(
				test.id,
				json.RawMessage(test.before),
				json.RawMessage(test.after),
			)
			if errors.Is(err, devices.ErrEntityInterpretationImmutable) != test.changed ||
				(err != nil && !test.changed) {
				t.Fatalf("error=%v changed=%v", err, test.changed)
			}
		})
	}
}

func TestResolveQuantityKind(t *testing.T) {
	t.Parallel()
	field, _ := devices.BuiltinStateField(devices.EntityTypeNumericsensorV1, "")
	for _, test := range []struct {
		support, kind string
		invalid       bool
	}{
		{`{"state":{"unit":"%"}}`, "", false},
		{`{"state":{"unit":"%","quantity_kind":"battery_level"}}`, "battery_level", false},
		{`{"state":{"unit":"V","quantity_kind":"battery_level"}}`, "", true},
		{`{"state":{"unit":"%","quantity_kind":"unknown"}}`, "", true},
		{`{"state":{"unit":"%","quantity_kind":null}}`, "", true},
	} {
		got, err := devices.ResolveQuantity(field.Value.(devices.NumericField), json.RawMessage(test.support))
		if (err != nil) != test.invalid || (!test.invalid && got.Kind != test.kind) {
			t.Errorf("%s: %+v, %v", test.support, got, err)
		}
	}
	fixed, _ := devices.BuiltinStateField(devices.EntityTypeTemperatureV1, "")
	got, err := devices.ResolveQuantity(fixed.Value.(devices.NumericField), json.RawMessage(`{}`))
	if err != nil || got.Kind != "temperature" {
		t.Fatalf("fixed kind: %+v, %v", got, err)
	}
}
