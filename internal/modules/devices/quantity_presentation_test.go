package devices_test

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

func quantityView(id devices.EntityTypeID, support, value string) devices.EntityWithState {
	view := devices.EntityWithState{Entity: devices.Entity{TypeID: id, Support: devices.EntitySupport(support)}}
	if value != "" {
		view.State = &devices.State{Value: devices.Value(value), ObservationID: "observation-evidence"}
	}
	return view
}

func TestQuantityPresentationExamples(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, support, value, unit, label string
		id                                devices.EntityTypeID
		want                              float64
		digits                            int
	}{
		{name: "temperature", id: devices.EntityTypeTemperatureV1, support: `{}`, value: `21500`, want: 70.7, unit: "[degF]", label: "°F", digits: 1},
		{name: "negative temperature", id: devices.EntityTypeTemperatureV1, support: `{}`, value: `-40000`, want: -40, unit: "[degF]", label: "°F", digits: 1},
		{name: "speed", id: devices.EntityTypeSpeedV1, support: `{}`, value: `3.4`, want: 7.605583392984967, unit: "[mi_i]/h", label: "mi/h", digits: 1},
		{name: "pressure", id: devices.EntityTypePressureV1, support: `{}`, value: `1013.2`, want: 29.919866023257644, unit: "[in_i'Hg]", label: "in\u00a0Hg", digits: 2},
		{name: "rain depth", id: devices.EntityTypeNumericsensorV1, support: `{"state":{"unit":"mm","quantity_kind":"precipitation_depth","minimum":0,"maximum":100}}`, value: `2.54`, want: 0.1, unit: "[in_i]", label: "in", digits: 2},
		{name: "rain rate", id: devices.EntityTypeNumericsensorV1, support: `{"state":{"unit":"mm/h","quantity_kind":"precipitation_rate","minimum":0,"maximum":100}}`, value: `2.54`, want: 0.1, unit: "[in_i]/h", label: "in/h", digits: 2},
		{name: "unclassified rate", id: devices.EntityTypeNumericsensorV1, support: `{"state":{"unit":"mm/h","minimum":0,"maximum":100}}`, value: `2.54`, want: 2.54, unit: "mm/h", label: "mm/h", digits: 1},
		{name: "integer percentage", id: devices.EntityTypeBrightnessV1, support: `{}`, value: `80`, want: 80, unit: "%", label: "%", digits: 0},
		{name: "humidity", id: devices.EntityTypeRelativehumidityV1, support: `{}`, value: `45.123`, want: 45.123, unit: "%", label: "%", digits: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			view := quantityView(test.id, test.support, test.value)
			presenter := devices.NewPresenter(household.UnitSystemUS, devices.TableConverter{})
			result, quantities, err := presenter.Entity(view, "")
			if err != nil {
				t.Fatal(err)
			}
			var got float64
			if decodeErr := json.Unmarshal(result.State.Value, &got); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if math.Abs(got-test.want) > 1e-10 {
				t.Fatalf("got %.16g, want %.16g", got, test.want)
			}
			meta := quantities[""]
			if meta.Unit != test.unit || meta.Label != test.label || meta.FractionDigits != test.digits {
				t.Fatalf("metadata = %+v", meta)
			}
			if string(view.State.Value) != test.value || result.State.ObservationID != view.State.ObservationID {
				t.Fatal("canonical data/evidence changed")
			}
		})
	}
}

func TestQuantityCanonicalAndIdentityPreserveNumericTokens(t *testing.T) {
	t.Parallel()
	for _, system := range []household.UnitSystem{household.UnitSystemMetric, household.UnitSystemUS} {
		presenter := devices.NewPresenter(system, devices.TableConverter{})
		for _, test := range []struct {
			id             devices.EntityTypeID
			support, value string
			units          devices.ReadUnits
		}{
			{devices.EntityTypeTemperatureV1, `{}`, `21500`, devices.UnitsCanonical},
			{devices.EntityTypeColorxyV1, `{}`, `{"active":true,"x":3127,"y":3290}`, devices.UnitsCanonical},
			{devices.EntityTypeNumericsensorV1, `{"state":{"unit":"opaque","minimum":0,"maximum":1e30}}`, `900719925474099312345.0001`, ""},
			{devices.EntityTypeNumericsensorV1, `{"state":{"unit":"opaque","minimum":0,"maximum":1e30}}`, `9.00719925474099312345e20`, devices.UnitsCanonical},
		} {
			view := quantityView(test.id, test.support, test.value)
			got, _, err := presenter.Entity(view, test.units)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.State.Value) != test.value {
				t.Fatalf("numeric tokens changed: %s", got.State.Value)
			}
		}
	}
}

func TestQuantityColorCoordinatesOptionalFieldsAndMissingState(t *testing.T) {
	t.Parallel()
	presenter := devices.NewPresenter(household.UnitSystemUS, devices.TableConverter{})
	view := quantityView(devices.EntityTypeColorxyV1, `{}`, `{"active":true,"x":3127,"y":3290}`)
	result, quantities, err := presenter.Entity(view, "")
	if err != nil {
		t.Fatal(err)
	}
	var object struct {
		Active bool    `json:"active"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
	}
	if decodeErr := json.Unmarshal(result.State.Value, &object); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if math.Abs(object.X-0.3127) > 1e-12 || math.Abs(object.Y-0.329) > 1e-12 || !object.Active {
		t.Fatalf("color projection = %s", result.State.Value)
	}
	if quantities["/x"].Unit != "1" || quantities["/x"].FractionDigits != 4 || quantities["/y"].Range != nil {
		t.Fatal(quantities)
	}
	choice := quantityView(
		devices.EntityTypeNumericsettingV1,
		`{"state":{"unit":"mired","quantity_kind":"color_temperature","minimum":142,"maximum":454}}`,
		`{"mode":"choice","choice":"previous"}`,
	)
	got, quantities, err := presenter.Entity(choice, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.State.Value) != string(choice.State.Value) || quantities["/value"].Unit != "mired" {
		t.Fatalf("choice changed: %s %+v %v", got.State.Value, quantities, err)
	}
	missing := quantityView(devices.EntityTypeTemperatureV1, `{}`, "")
	got, quantities, err = presenter.Entity(missing, "")
	if err != nil || got.State != nil || quantities[""].Range != nil || quantities[""].Unit != "[degF]" {
		t.Fatalf("missing State metadata: %+v %+v %v", got, quantities, err)
	}
	rain := quantityView(
		devices.EntityTypeNumericsensorV1,
		`{"state":{"unit":"mm","quantity_kind":"precipitation_depth","minimum":0,"maximum":254}}`,
		"",
	)
	_, quantities, err = presenter.Entity(rain, "")
	if err != nil || quantities[""].Range == nil || quantities[""].Range.Maximum != 10 {
		t.Fatalf("projected declared bounds: %+v %v", quantities, err)
	}
	_, quantities, err = presenter.Entity(rain, devices.UnitsCanonical)
	if err != nil || quantities[""].Range == nil || quantities[""].Range.Minimum != 0 ||
		quantities[""].Range.Maximum != 254 {
		t.Fatalf("canonical bounds: %+v %v", quantities, err)
	}
}

func TestHistoryProjectionPreservesGapsAndCanonicalResult(t *testing.T) {
	t.Parallel()
	presenter := devices.NewPresenter(household.UnitSystemUS, devices.TableConverter{})
	view := quantityView(devices.EntityTypeTemperatureV1, `{}`, "")
	metrics := devices.HistoryMetricValues{
		"low":  json.RawMessage(`20000`),
		"high": json.RawMessage(`30000`),
		"mean": json.RawMessage(`25000`),
	}
	for _, data := range []devices.HistoryResult{
		devices.HistorySummaryResult{Metrics: metrics},
		devices.HistoryBucketedSummaryResult{Buckets: []devices.HistorySummaryBucket{{Metrics: metrics}, {Metrics: devices.HistoryMetricValues{"low": json.RawMessage(`null`), "mean": json.RawMessage(`null`)}}}},
	} {
		original := devices.HistoryQueryResult{Data: data}
		snapshot := string(metrics["low"]) + string(metrics["high"]) + string(metrics["mean"])
		got, quantity, err := presenter.History(view, "", original, "")
		if err != nil || quantity.Unit != "[degF]" {
			t.Fatalf("projection: %+v %v", quantity, err)
		}
		var projected devices.HistoryMetricValues
		switch result := got.Data.(type) {
		case devices.HistorySummaryResult:
			projected = result.Metrics
		case devices.HistoryBucketedSummaryResult:
			projected = result.Buckets[0].Metrics
			if string(result.Buckets[1].Metrics["low"]) != "null" ||
				string(result.Buckets[1].Metrics["mean"]) != "null" {
				t.Fatal("gap became a number")
			}
		}
		for label, want := range map[string]string{"low": "68", "high": "86", "mean": "77"} {
			if string(projected[label]) != want {
				t.Fatalf("%s = %s, want %s", label, projected[label], want)
			}
		}
		after := string(metrics["low"]) + string(metrics["high"]) + string(metrics["mean"])
		if snapshot != after {
			t.Fatal("canonical result mutated")
		}
		canonical, _, err := presenter.History(view, "", original, devices.UnitsCanonical)
		if err != nil || !reflect.DeepEqual(canonical, original) {
			t.Fatalf("canonical history changed: %v", err)
		}
	}
}

func TestQuantityConverterRejectsInvalidMathematics(t *testing.T) {
	t.Parallel()
	for _, conversion := range []devices.Affine{
		{Scale: 0}, {Scale: -1}, {Scale: math.Inf(1)}, {Scale: math.NaN()}, {Scale: 1, Offset: math.Inf(1)},
	} {
		if _, err := conversion.Apply(1); err == nil {
			t.Fatal("invalid conversion accepted", conversion)
		}
	}
	for _, number := range []float64{math.Inf(1), math.NaN(), math.MaxFloat64} {
		if _, err := (devices.Affine{Scale: 2}).Apply(number); err == nil {
			t.Fatal("invalid/overflowing result accepted")
		}
	}
	for _, pair := range [][2]string{{"[degF]", "Cel"}, {"mm/h", "[mi_i]/h"}, {"/MK", "K"}} {
		if _, err := (devices.TableConverter{}).Affine(pair[0], pair[1]); err == nil {
			t.Fatal("unsupported table pair accepted", pair)
		}
	}
	if _, err := devices.ParseReadUnits("us"); err == nil {
		t.Fatal("per-read household override accepted")
	}
}

type invalidQuantityConverter struct{}

func (invalidQuantityConverter) Affine(string, string) (devices.Affine, error) {
	return devices.Affine{Scale: -1, Offset: 32}, nil
}

func TestQuantityPresenterValidatesReplacementEngineAndSupport(t *testing.T) {
	t.Parallel()
	view := quantityView(devices.EntityTypeTemperatureV1, `{}`, `21500`)
	presenter := devices.NewPresenter(household.UnitSystemUS, invalidQuantityConverter{})
	if _, _, err := presenter.Entity(view, ""); err == nil {
		t.Fatal("replacement engine admitted a negative scale")
	}
	// Canonical reads must not need the display engine, including a replacement
	// that cannot supply a valid conversion.
	canonical, _, err := presenter.Entity(view, devices.UnitsCanonical)
	if err != nil || string(canonical.State.Value) != "21500" {
		t.Fatalf("canonical read used display engine: %v", err)
	}
	for _, support := range []string{
		`{"state":{"unit":"mm/h","quantity_kind":"speed","minimum":0,"maximum":10}}`,
		`{"state":{"unit":"mm/h","quantity_kind":"unknown","minimum":0,"maximum":10}}`,
	} {
		presenter = devices.NewPresenter(household.UnitSystemUS, devices.TableConverter{})
		if _, _, supportErr := presenter.Entity(
			quantityView(devices.EntityTypeNumericsensorV1, support, `1`),
			"",
		); supportErr == nil {
			t.Fatal("invalid retained kind/unit was silently displayed")
		}
	}
}

func TestMetricTemperaturePrecisionDoesNotRoundReading(t *testing.T) {
	t.Parallel()
	presenter := devices.NewPresenter(household.UnitSystemMetric, devices.TableConverter{})
	view := quantityView(devices.EntityTypeTemperatureV1, `{}`, `21501`)
	result, quantities, err := presenter.Entity(view, "")
	if err != nil {
		t.Fatal(err)
	}
	var value float64
	if decodeErr := json.Unmarshal(result.State.Value, &value); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if math.Abs(value-21.501) > 1e-12 || quantities[""].Unit != "Cel" || quantities[""].FractionDigits != 1 {
		t.Fatalf("metric presentation rounded or mislabeled: %s %+v", result.State.Value, quantities)
	}
}

func TestQuantitySingleValueExtentIsOmittedAcrossRepresentations(t *testing.T) {
	t.Parallel()
	presenter := devices.NewPresenter(household.UnitSystemUS, devices.TableConverter{})
	view := quantityView(
		devices.EntityTypeNumericsensorV1,
		`{"state":{"unit":"mm","quantity_kind":"precipitation_depth","minimum":2.54,"maximum":2.54}}`,
		`2.54`,
	)
	for _, units := range []devices.ReadUnits{"", devices.UnitsCanonical} {
		result, metadata, err := presenter.Entity(view, units)
		if err != nil || result.State == nil || metadata[""].Range != nil {
			t.Fatalf("valid single-value extent made State unreadable: %+v %+v %v", result, metadata, err)
		}
		history := devices.HistoryQueryResult{
			Data: devices.HistorySummaryResult{Metrics: devices.HistoryMetricValues{"low": json.RawMessage(`2.54`)}},
		}
		_, quantity, historyErr := presenter.History(view, "", history, units)
		if historyErr != nil || quantity.Range != nil {
			t.Fatalf("single-value history extent: %+v %v", quantity, historyErr)
		}
	}
}
