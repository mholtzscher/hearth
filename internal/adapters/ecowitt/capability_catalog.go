package ecowitt

import (
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// deviceSlot is one configured Device slot. V1 models slots rather than
// replaceable radio hardware identity, because an Ecowitt report carries no
// stable attached-sensor identifier.
type deviceSlot string

const (
	// gatewaySlot is the GW2000 gateway slot.
	gatewaySlot deviceSlot = "gateway"
	// outdoorArraySlot is the WS90 outdoor array slot.
	outdoorArraySlot deviceSlot = "outdoor-array"
	// deviceKindSensor is the only registered Device kind.
	deviceKindSensor = "sensor"
)

// quantityKind selects the generated Entity-type facade that owns one
// canonical measurement. The kind is the Adapter's only link between a vendor
// field, a canonical unit, and a typed Observation.
type quantityKind uint8

const (
	quantityTemperature quantityKind = iota
	quantityRelativeHumidity
	quantityPressure
	quantitySpeed
	quantityNumericSensor
)

// normalizedState is one source value normalized into the canonical value its
// Entity facade accepts. MilliCelsius is set for temperature; Value is set for
// every other kind, in that Entity's canonical unit.
type normalizedState struct {
	Kind         quantityKind
	MilliCelsius int64
	Value        float64
}

// measurementPlan is one catalog entry: the vendor field, the canonical Entity
// it feeds, the registered descriptor, and the decoder that normalizes the
// source decimal. The catalog is the one definition site for registration
// order, field ownership, support, and decoder selection.
type measurementPlan struct {
	Slot       deviceSlot
	Field      string
	EntityKey  string
	EntityName string
	Kind       quantityKind
	Unit       string
	Minimum    float64
	Maximum    float64
	Descriptor adapter.EntityDescriptor
	Decode     func(string) (normalizedState, error)
}

// Entity and Device external ID bounds from the identity contract.
const (
	maximumDeviceNameRunes = 128
)

// Canonical unit values fixed by the catalog contract. Pressure, relative
// humidity, and speed units are owned by their reusable Entity types; the
// generic numeric sensor carries its unit in Entity support.
const (
	unitMilliCelsius        = "mCel"
	unitPercent             = "%"
	unitHectopascals        = "hPa"
	unitMetresPerSecond     = "m/s"
	unitDegrees             = "deg"
	unitWattsPerSquareMetre = "W/m\u00b2"
	unitIndex               = "index"
	unitMillimetres         = "mm"
	unitMillimetresPerHour  = "mm/h"
)

// ecowittMeasurementCatalog returns the closed v1 catalog in registration
// order: the gateway slot's table order followed by the outdoor array slot's
// table order. Building the catalog compiles each plan's generated Entity
// descriptor, so a schema failure is reported instead of panicking.
//
//nolint:mnd // Catalog bounds and units are capability data, not algorithmic constants.
func ecowittMeasurementCatalog() ([]measurementPlan, error) {
	plans := []measurementPlan{
		gatewayTemperaturePlan(),
		gatewayHumidityPlan(),
		gatewayRelativePressurePlan(),
		gatewayAbsolutePressurePlan(),
		outdoorTemperaturePlan(),
		outdoorHumidityPlan(),
		numericPlan(
			"winddir", "wind-direction", "Wind Direction",
			unitDegrees, 360, decodeCanonicalNumeric(360),
		),
		speedPlan(outdoorArraySlot, "windspeedmph", "wind-speed", "Wind Speed"),
		speedPlan(outdoorArraySlot, "windgustmph", "wind-gust", "Wind Gust"),
		speedPlan(outdoorArraySlot, "maxdailygust", "maximum-daily-gust", "Maximum Daily Gust"),
		numericPlan(
			"solarradiation", "solar-radiation", "Solar Radiation",
			unitWattsPerSquareMetre, 10000, decodeCanonicalNumeric(10000),
		),
		numericPlan(
			"uv", "uv-index", "UV Index",
			unitIndex, 100, decodeCanonicalNumeric(100),
		),
		numericPlan(
			"rrain_piezo", "rain-rate", "Rain Rate",
			unitMillimetresPerHour, 10000, decodeInchScaledNumeric(10000),
		),
		numericPlan(
			"erain_piezo", "event-rain", "Event Rain",
			unitMillimetres, 10000000, decodeInchScaledNumeric(10000000),
		),
		numericPlan(
			"hrain_piezo", "hourly-rain", "Hourly Rain",
			unitMillimetres, 10000000, decodeInchScaledNumeric(10000000),
		),
		numericPlan(
			"drain_piezo", "daily-rain", "Daily Rain",
			unitMillimetres, 10000000, decodeInchScaledNumeric(10000000),
		),
		numericPlan(
			"wrain_piezo", "weekly-rain", "Weekly Rain",
			unitMillimetres, 10000000, decodeInchScaledNumeric(10000000),
		),
		numericPlan(
			"mrain_piezo", "monthly-rain", "Monthly Rain",
			unitMillimetres, 10000000, decodeInchScaledNumeric(10000000),
		),
		numericPlan(
			"yrain_piezo", "yearly-rain", "Yearly Rain",
			unitMillimetres, 10000000, decodeInchScaledNumeric(10000000),
		),
	}
	built, err := registerPlanDescriptors(plans)
	if err != nil {
		return nil, err
	}
	return built, nil
}

// gatewayTemperaturePlan registers indoor temperature in integer milli-Celsius.
func gatewayTemperaturePlan() measurementPlan {
	return measurementPlan{
		Slot: gatewaySlot, Field: "tempinf", EntityKey: "indoor-temperature", EntityName: "Indoor Temperature",
		Kind: quantityTemperature, Unit: unitMilliCelsius,
		Minimum: temperatureMinimumMilliCelsius, Maximum: temperatureMaximumMilliCelsius,
		Decode: decodeFahrenheitTemperature,
	}
}

// gatewayHumidityPlan registers indoor humidity in decimal percent.
func gatewayHumidityPlan() measurementPlan {
	return measurementPlan{
		Slot: gatewaySlot, Field: "humidityin", EntityKey: "indoor-humidity", EntityName: "Indoor Humidity",
		Kind: quantityRelativeHumidity, Unit: unitPercent,
		Minimum: relativeHumidityMinimumPercent, Maximum: relativeHumidityMaximumPercent,
		Decode: decodeRelativeHumidityPercent,
	}
}

// gatewayRelativePressurePlan registers sea-level relative pressure in decimal
// hectopascals.
func gatewayRelativePressurePlan() measurementPlan {
	return measurementPlan{
		Slot: gatewaySlot, Field: "baromrelin", EntityKey: "relative-pressure", EntityName: "Relative Pressure",
		Kind: quantityPressure, Unit: unitHectopascals,
		Minimum: pressureMinimumHectopascals, Maximum: pressureMaximumHectopascals,
		Decode: decodeInchOfMercuryPressure,
	}
}

// gatewayAbsolutePressurePlan registers station absolute pressure in decimal
// hectopascals.
func gatewayAbsolutePressurePlan() measurementPlan {
	return measurementPlan{
		Slot: gatewaySlot, Field: "baromabsin", EntityKey: "absolute-pressure", EntityName: "Absolute Pressure",
		Kind: quantityPressure, Unit: unitHectopascals,
		Minimum: pressureMinimumHectopascals, Maximum: pressureMaximumHectopascals,
		Decode: decodeInchOfMercuryPressure,
	}
}

// outdoorTemperaturePlan registers outdoor temperature in integer
// milli-Celsius.
func outdoorTemperaturePlan() measurementPlan {
	return measurementPlan{
		Slot: outdoorArraySlot, Field: "tempf", EntityKey: "outdoor-temperature", EntityName: "Outdoor Temperature",
		Kind: quantityTemperature, Unit: unitMilliCelsius,
		Minimum: temperatureMinimumMilliCelsius, Maximum: temperatureMaximumMilliCelsius,
		Decode: decodeFahrenheitTemperature,
	}
}

// outdoorHumidityPlan registers outdoor humidity in decimal percent.
func outdoorHumidityPlan() measurementPlan {
	return measurementPlan{
		Slot: outdoorArraySlot, Field: "humidity", EntityKey: "outdoor-humidity", EntityName: "Outdoor Humidity",
		Kind: quantityRelativeHumidity, Unit: unitPercent,
		Minimum: relativeHumidityMinimumPercent, Maximum: relativeHumidityMaximumPercent,
		Decode: decodeRelativeHumidityPercent,
	}
}

// speedPlan registers one miles-per-hour wind measurement as decimal metres per
// second.
func speedPlan(slot deviceSlot, field, key, name string) measurementPlan {
	return measurementPlan{
		Slot: slot, Field: field, EntityKey: key, EntityName: name,
		Kind: quantitySpeed, Unit: unitMetresPerSecond,
		Minimum: speedMinimumMetersPerSecond, Maximum: speedMaximumMetersPerSecond,
		Decode: decodeMilesPerHourSpeed,
	}
}

// numericPlan registers one generic numeric-sensor measurement with its
// canonical unit and validation envelope.
func numericPlan(
	field, key, name, unit string,
	maximum float64,
	decode func(string) (normalizedState, error),
) measurementPlan {
	return measurementPlan{
		Slot: outdoorArraySlot, Field: field, EntityKey: key, EntityName: name,
		Kind: quantityNumericSensor, Unit: unit, Minimum: 0, Maximum: maximum,
		Decode: decode,
	}
}

// registerPlanDescriptors fills every plan's Entity descriptor from the
// generated facade that owns its Entity type. Descriptors are built after the
// catalog table so the table stays the single ordering and ownership site.
func registerPlanDescriptors(plans []measurementPlan) ([]measurementPlan, error) {
	registered := make([]measurementPlan, len(plans))
	for index, plan := range plans {
		descriptor, err := newQuantityDescriptor(plan.Kind, adapter.EntityMetadata{
			Key:        plan.EntityKey,
			ExternalID: entityExternalID(plan.Slot, plan.EntityKey),
			Name:       plan.EntityName,
		}, plan.Unit, plan.Minimum, plan.Maximum)
		if err != nil {
			return nil, err
		}
		plan.Descriptor = descriptor
		registered[index] = plan
	}
	return registered, nil
}

// entityExternalID is the canonical Entity external ID inside one Device slot:
// the Device external ID joined to the Entity key. No vendor identity, PASSKEY,
// topic, firmware, or display name participates.
func entityExternalID(slot deviceSlot, entityKey string) string {
	return string(slot) + "/" + entityKey
}

// plansForSlot returns the catalog plans owned by one Device slot, preserving
// catalog order.
func plansForSlot(plans []measurementPlan, slot deviceSlot) []measurementPlan {
	owned := make([]measurementPlan, 0, len(plans))
	for _, plan := range plans {
		if plan.Slot == slot {
			owned = append(owned, plan)
		}
	}
	return owned
}
