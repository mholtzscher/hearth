package zigbee2mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This file defines the closed Go strategy registry for Zigbee2MQTT JSON
// profiles. Profiles state mapping intent by naming one of exactly 12
// strategies; Go retains every eligibility rule, identity rule, support
// document, State decoder, Command translator, matcher, and outcome policy.
// CompileParameters runs once at catalog compilation and Plan receives the
// typed compiled parameters, never unvalidated JSON. Plan reports false when
// the upstream expose is ineligible, omitting only that candidate.
//
// Every strategy reuses the existing entity constructors and expose-index
// helpers, so profile planning preserves the established discovery behavior
// byte-for-byte. There is no parallel runtime behavior here: only parameter compilation, strategy-owned eligibility
// gating, and identity assembly from the profile-supplied base key and name.

// Closed profile strategy parameter values. Profiles carry these exact
// strings; unknown values fail catalog compilation.
const (
	profileNumericSensorFormatInteger            = "integer"
	profileNumericSensorFormatFloat              = "float"
	profileNumericSensorBoundsFixed              = "fixed"
	profileNumericSensorBoundsUpstreamOrFallback = "upstream-or-fallback"
	profileEnumActionAccessSetOnly               = "set-only"
	profileEnumActionAccessIncludesSet           = "includes-set"
)

// Closed profile strategy unit bounds. Accepted unit sets hold 1-4 unique
// exact strings; every unit string holds at most 32 characters.
const (
	profileStrategyMaxAcceptedUnits = 4
	profileStrategyMaxUnitLength    = 32
)

// emptyProfileStrategyParameters is the compiled form for strategies with no
// profile-controlled parameters. The compiler accepts exactly {}.
type emptyProfileStrategyParameters struct{}

// numericSensorStrategyParameters is the compiled form for the generic
// numeric-sensor strategy. AcceptedUnits holds 1-4 unique exact upstream
// units where "" requires the upstream unit to be absent; Unit is the
// Hearth support unit; NumberFormat selects exact-integer or finite-float
// decoding; Bounds carries the fixed or upstream-or-fallback envelope with
// finite Minimum strictly below Maximum.
type numericSensorStrategyParameters struct {
	AcceptedUnits []string
	Unit          string
	NumberFormat  string
	Bounds        numericSensorStrategyBounds
}

// numericSensorStrategyBounds is one compiled bound envelope for the generic
// numeric-sensor strategy. Mode is fixed or upstream-or-fallback.
type numericSensorStrategyBounds struct {
	Mode    string
	Minimum float64
	Maximum float64
}

// numericSettingStrategyParameters is the compiled form for the generic
// numeric-setting strategy. AcceptedUnits follows the same exact-match rules
// as numeric-sensor; Unit is nil when the profile omits it, in which case
// Hearth support omits its optional unit.
type numericSettingStrategyParameters struct {
	AcceptedUnits []string
	Unit          *string
}

// enumActionStrategyParameters is the compiled form for the generic
// enum-action strategy. Access is set-only or includes-set.
type enumActionStrategyParameters struct {
	Access string
}

// defaultProfileStrategyRegistry returns the closed production strategy set
// profiles may reference. Every entry name matches its Definition.Name; the
// catalog compiler rejects unknown names and strategy/source mismatches.
func defaultProfileStrategyRegistry() profileStrategyRegistry {
	return profileStrategyRegistry{
		profileStrategyBinaryPower: {
			Name:               profileStrategyBinaryPower,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceFeature},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planBinaryPowerProfileStrategy,
		},
		profileStrategyBrightness: {
			Name:               profileStrategyBrightness,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceFeature},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planBrightnessProfileStrategy,
		},
		profileStrategyColorTemperature: {
			Name:               profileStrategyColorTemperature,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceFeature},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planColorTemperatureProfileStrategy,
		},
		profileStrategyColorXY: {
			Name:               profileStrategyColorXY,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceFeature},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planColorXYProfileStrategy,
		},
		profileStrategyColorHS: {
			Name:               profileStrategyColorHS,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceFeature},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planColorHSProfileStrategy,
		},
		profileStrategyColorMode: {
			Name:               profileStrategyColorMode,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceDerived},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planColorModeProfileStrategy,
		},
		profileStrategyStartupColorTemperature: {
			Name:               profileStrategyStartupColorTemperature,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceFeature},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planStartupColorTemperatureProfileStrategy,
		},
		profileStrategyTemperature: {
			Name:               profileStrategyTemperature,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceRoot},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planTemperatureProfileStrategy,
		},
		profileStrategyNumericSensor: {
			Name:               profileStrategyNumericSensor,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceRoot},
			CompileParameters:  compileNumericSensorProfileStrategyParameters,
			Plan:               planNumericSensorProfileStrategy,
		},
		profileStrategyNumericSetting: {
			Name:               profileStrategyNumericSetting,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceRoot},
			CompileParameters:  compileNumericSettingProfileStrategyParameters,
			Plan:               planNumericSettingProfileStrategy,
		},
		profileStrategyEnumSetting: {
			Name:               profileStrategyEnumSetting,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceRoot},
			CompileParameters:  compileEmptyProfileStrategyParameters,
			Plan:               planEnumSettingProfileStrategy,
		},
		profileStrategyEnumAction: {
			Name:               profileStrategyEnumAction,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceRoot},
			CompileParameters:  compileEnumActionProfileStrategyParameters,
			Plan:               planEnumActionProfileStrategy,
		},
	}
}

// compileEmptyProfileStrategyParameters accepts exactly the empty parameter
// object. Any field, array, scalar, or null value fails catalog compilation.
func compileEmptyProfileStrategyParameters(raw json.RawMessage) (any, error) {
	var fields map[string]json.RawMessage
	if err := decodeClosedProfileStrategyParameters(raw, &fields); err != nil {
		return nil, err
	}
	if len(fields) != 0 {
		return nil, fmt.Errorf("strategy expects exactly {}, got %d fields", len(fields))
	}
	return emptyProfileStrategyParameters{}, nil
}

// compileNumericSensorProfileStrategyParameters compiles the generic
// numeric-sensor parameters once at catalog load. Bounds are semantic:
// both bounds must be finite with minimum strictly below maximum, which the
// JSON Schema cannot compare across fields.
func compileNumericSensorProfileStrategyParameters(raw json.RawMessage) (any, error) {
	var wire struct {
		AcceptedUnits []string `json:"accepted_units"`
		Unit          string   `json:"unit"`
		NumberFormat  string   `json:"number_format"`
		Bounds        struct {
			Mode    string          `json:"mode"`
			Minimum json.RawMessage `json:"minimum"`
			Maximum json.RawMessage `json:"maximum"`
		} `json:"bounds"`
	}
	if err := decodeClosedProfileStrategyParameters(raw, &wire); err != nil {
		return nil, err
	}
	if err := validateAcceptedProfileStrategyUnits(wire.AcceptedUnits); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(wire.Unit) == 0 ||
		utf8.RuneCountInString(wire.Unit) > profileStrategyMaxUnitLength {
		return nil, fmt.Errorf("numeric-sensor unit must be 1-%d characters, got %q",
			profileStrategyMaxUnitLength, wire.Unit)
	}
	switch wire.NumberFormat {
	case profileNumericSensorFormatInteger, profileNumericSensorFormatFloat:
	default:
		return nil, fmt.Errorf("numeric-sensor number_format must be integer or float, got %q",
			wire.NumberFormat)
	}
	switch wire.Bounds.Mode {
	case profileNumericSensorBoundsFixed, profileNumericSensorBoundsUpstreamOrFallback:
	default:
		return nil, fmt.Errorf("numeric-sensor bounds mode must be fixed or upstream-or-fallback, got %q",
			wire.Bounds.Mode)
	}
	minimum, err := decodeRequiredProfileStrategyNumber(wire.Bounds.Minimum, "minimum")
	if err != nil {
		return nil, fmt.Errorf("numeric-sensor bounds: %w", err)
	}
	maximum, err := decodeRequiredProfileStrategyNumber(wire.Bounds.Maximum, "maximum")
	if err != nil {
		return nil, fmt.Errorf("numeric-sensor bounds: %w", err)
	}
	if boundsErr := validateFiniteProfileStrategyBounds(minimum, maximum); boundsErr != nil {
		return nil, fmt.Errorf("numeric-sensor bounds: %w", boundsErr)
	}
	return numericSensorStrategyParameters{
		AcceptedUnits: append([]string(nil), wire.AcceptedUnits...),
		Unit:          wire.Unit,
		NumberFormat:  wire.NumberFormat,
		Bounds: numericSensorStrategyBounds{
			Mode:    wire.Bounds.Mode,
			Minimum: minimum,
			Maximum: maximum,
		},
	}, nil
}

// compileNumericSettingProfileStrategyParameters compiles the generic
// numeric-setting parameters once at catalog load. Unit is optional: when
// omitted, Hearth support omits its optional unit.
func compileNumericSettingProfileStrategyParameters(raw json.RawMessage) (any, error) {
	var wire struct {
		AcceptedUnits []string        `json:"accepted_units"`
		Unit          json.RawMessage `json:"unit"`
	}
	if err := decodeClosedProfileStrategyParameters(raw, &wire); err != nil {
		return nil, err
	}
	if err := validateAcceptedProfileStrategyUnits(wire.AcceptedUnits); err != nil {
		return nil, err
	}
	var unit *string
	if len(wire.Unit) > 0 {
		// An explicit null is not omission: the contract has no null
		// substitute for an omitted optional field.
		var value string
		if err := json.Unmarshal(wire.Unit, &value); err != nil {
			return nil, fmt.Errorf("numeric-setting unit must be a string: %w", err)
		}
		if utf8.RuneCountInString(value) == 0 ||
			utf8.RuneCountInString(value) > profileStrategyMaxUnitLength {
			return nil, fmt.Errorf("numeric-setting unit must be 1-%d characters, got %q",
				profileStrategyMaxUnitLength, value)
		}
		unit = &value
	}
	return numericSettingStrategyParameters{
		AcceptedUnits: append([]string(nil), wire.AcceptedUnits...),
		Unit:          unit,
	}, nil
}

// compileEnumActionProfileStrategyParameters compiles the generic enum-action
// parameters once at catalog load. Access is exactly set-only (access must
// equal the set bit) or includes-set (the set bit plus any other bits).
func compileEnumActionProfileStrategyParameters(raw json.RawMessage) (any, error) {
	var wire struct {
		Access string `json:"access"`
	}
	if err := decodeClosedProfileStrategyParameters(raw, &wire); err != nil {
		return nil, err
	}
	switch wire.Access {
	case profileEnumActionAccessSetOnly, profileEnumActionAccessIncludesSet:
	default:
		return nil, fmt.Errorf("enum-action access must be set-only or includes-set, got %q", wire.Access)
	}
	return enumActionStrategyParameters{Access: wire.Access}, nil
}

// validateAcceptedProfileStrategyUnits enforces the shared exact-match unit
// contract: 1-4 unique strings of at most 32 characters, where "" requires
// the upstream unit to be absent. No implicit conversion ever occurs.
func validateAcceptedProfileStrategyUnits(units []string) error {
	if len(units) < 1 || len(units) > profileStrategyMaxAcceptedUnits {
		return fmt.Errorf("accepted_units must hold 1-%d units, got %d",
			profileStrategyMaxAcceptedUnits, len(units))
	}
	seen := make(map[string]struct{}, len(units))
	for _, unit := range units {
		if utf8.RuneCountInString(unit) > profileStrategyMaxUnitLength {
			return fmt.Errorf("accepted unit %q exceeds %d characters", unit, profileStrategyMaxUnitLength)
		}
		if _, duplicate := seen[unit]; duplicate {
			return fmt.Errorf("accepted unit %q is duplicated", unit)
		}
		seen[unit] = struct{}{}
	}
	return nil
}

// decodeRequiredProfileStrategyNumber decodes one required JSON number while
// distinguishing omission and null from zero. The JSON decoder rejects
// non-finite literals; Float64 also rejects values outside binary64 range.
func decodeRequiredProfileStrategyNumber(raw json.RawMessage, name string) (float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, fmt.Errorf("%s must be a JSON number", name)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("decode %s: %w", name, err)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be a JSON number", name)
	}
	decoded, err := number.Float64()
	if err != nil || !isFinite(decoded) {
		return 0, fmt.Errorf("%s must be a finite JSON number", name)
	}
	return decoded, nil
}

// validateFiniteProfileStrategyBounds requires finite bounds with minimum
// strictly below maximum. JSON numbers are always finite on the wire, so
// this is the semantic rule the schema cannot express.
func validateFiniteProfileStrategyBounds(minimum, maximum float64) error {
	if !isFinite(minimum) || !isFinite(maximum) {
		return errors.New("bounds must both be finite numbers")
	}
	if minimum >= maximum {
		return fmt.Errorf("bounds minimum %v must be below maximum %v", minimum, maximum)
	}
	return nil
}

// decodeClosedProfileStrategyParameters decodes one parameter object with
// unknown fields rejected, so strategy parameters stay exact and closed.
// Missing required fields fail through the struct decode; null and trailing
// data fail here rather than silently compiling.
func decodeClosedProfileStrategyParameters(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("strategy parameters must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode strategy parameters: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("strategy parameters must be a single JSON object")
	}
	return nil
}

// profileStrategyEntityMetadata builds the canonical Entity identity for one
// strategy plan from the profile-supplied base key and display name. Go owns
// endpoint scoping and external IDs; profiles never construct suffixes.
// Property names never enter Binding or Entity keys.
func profileStrategyEntityMetadata(
	input profileStrategyInput,
) (adapter.EntityMetadata, bool) {
	key, name := scopedIdentity(
		input.Identity.Key,
		input.Identity.Name,
		input.Root.expose.Endpoint,
		input.Root.endpoint,
		input.Root.scoped,
	)
	if !validDescriptorName(name) {
		return adapter.EntityMetadata{}, false
	}
	return adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(input.Root) + "/" + input.Identity.Key,
		Name:       name,
	}, true
}

// profileColorModePolicy resolves one root's companion color-mode property
// and whether Hearth may trust it: exactly one resolved light root may
// expect the property, and no foreign expose may claim it under a different
// meaning. An ambiguous or colliding property omits the affected color and
// mode capabilities instead of guessing.
func profileColorModePolicy(index exposeIndex, root indexedExpose) (string, bool) {
	property := colorModeProperty(root)
	expectations := 0
	for _, other := range index.Roots(upstreamDeviceKindLight) {
		if other.resolved && colorModeProperty(other) == property {
			expectations++
		}
	}
	usable := expectations == 1 && !index.propertyHasForeignClaim(property)
	return property, usable
}

// planBinaryPowerProfileStrategy plans the shared publish/set/get power
// Entity for one binary state feature. Light and relay families share this
// behavior through the same power constructor.
func planBinaryPowerProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if input.Expose == nil {
		return entityPlan{}, false
	}
	feature := *input.Expose
	if !validPowerFeature(feature) || !input.Index.PropertyUnique(feature.Property) {
		return entityPlan{}, false
	}
	powerOn, onErr := canonicalScalar(feature.ValueOn)
	powerOff, offErr := canonicalScalar(feature.ValueOff)
	if onErr != nil || offErr != nil || powerOn.canonical == powerOff.canonical {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newPowerPlan(metadata, feature.Property, powerOn, powerOff)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planBrightnessProfileStrategy plans the percent-scaled brightness Entity
// for one numeric brightness feature with its exact discovered maximum.
func planBrightnessProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if input.Expose == nil {
		return entityPlan{}, false
	}
	feature := *input.Expose
	if !validBrightnessFeature(feature) || !input.Index.PropertyUnique(feature.Property) {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newBrightnessPlan(metadata, feature.Property, *feature.ValueMax)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planColorTemperatureProfileStrategy plans the mired color-temperature
// Entity for one numeric feature. Temperature needs a trustworthy companion
// mode property: a temperature-only root stays active when the mode is
// absent, while an advertised color composite keeps the root mode-sensitive
// with no silent always-active fallback.
func planColorTemperatureProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if input.Expose == nil {
		return entityPlan{}, false
	}
	feature := *input.Expose
	minimum, maximum, valid := colorTempRange(feature)
	if !valid || !input.Index.PropertyUnique(feature.Property) {
		return entityPlan{}, false
	}
	modeProperty, usable := profileColorModePolicy(input.Index, input.Root)
	if !usable {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newColorTempPlan(
		metadata, feature.Property, modeProperty, hasColorComposite(input.Root), minimum, maximum,
	)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planColorXYProfileStrategy plans the native XY chromaticity Entity for one
// color_xy composite with its companion mode property. XY is active exactly
// when the same-message mode is xy.
func planColorXYProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if input.Expose == nil {
		return entityPlan{}, false
	}
	feature := *input.Expose
	modeProperty, usable := profileColorModePolicy(input.Index, input.Root)
	if !usable {
		return entityPlan{}, false
	}
	if !validColorComposite(input.Index, input.Root, feature, []colorAxisSpec{
		{name: "x", maximum: maxRawXY},
		{name: "y", maximum: maxRawXY},
	}) {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newColorXYPlan(metadata, feature.Property, modeProperty)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planColorHSProfileStrategy plans the native hue/saturation Entity for one
// color_hs composite with its companion mode property. HS is active exactly
// when the same-message mode is hs.
func planColorHSProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if input.Expose == nil {
		return entityPlan{}, false
	}
	feature := *input.Expose
	modeProperty, usable := profileColorModePolicy(input.Index, input.Root)
	if !usable {
		return entityPlan{}, false
	}
	if !validColorComposite(input.Index, input.Root, feature, []colorAxisSpec{
		{name: "hue", maximum: maxRawHue},
		{name: "saturation", maximum: maxRawSaturation},
	}) {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newColorHSPlan(metadata, feature.Property, modeProperty)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planColorModeProfileStrategy plans the read-only derived color-mode
// companion for one root. The evaluator passes successfully planned color
// or temperature siblings as Prior; without at least one sibling plan the
// mode Entity is omitted. The strategy still owns the foreign-claim and
// ownership checks even though the production evaluator arrives later.
func planColorModeProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if len(input.Prior) == 0 {
		return entityPlan{}, false
	}
	modeProperty, usable := profileColorModePolicy(input.Index, input.Root)
	if !usable {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newColorModePlan(metadata, modeProperty)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planStartupColorTemperatureProfileStrategy plans the startup-temperature
// setting for one color_temp_startup feature, preserving the exact-integer
// mired bounds and the previous to 65535 sentinel mapping.
func planStartupColorTemperatureProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if input.Expose == nil {
		return entityPlan{}, false
	}
	feature := *input.Expose
	if feature.Property == "" || !exposeCanPublish(feature) || !exposeCanSet(feature) ||
		!exposeCanGet(feature) || !input.Index.PropertyUnique(feature.Property) {
		return entityPlan{}, false
	}
	minimum, maximum, valid := startupTempRange(feature)
	if !valid {
		return entityPlan{}, false
	}
	choices, valid := startupPreviousChoices(feature)
	if !valid {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newStartupColorTempPlan(metadata, feature.Property, minimum, maximum, choices)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planTemperatureProfileStrategy plans the read-only milli-Celsius
// temperature Entity for one numeric root expose. Get access alone controls
// startup refresh: a publish-only sensor has no get properties and no
// command translator, so it never creates a command route.
func planTemperatureProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if !input.Root.resolved || input.Root.expose.Type != upstreamExposeNumeric {
		return entityPlan{}, false
	}
	expose := input.Root.expose
	if expose.Property == "" || expose.Unit != temperatureUnitCelsius ||
		!exposeCanPublish(expose) || exposeCanSet(expose) ||
		!input.Index.PropertyUnique(expose.Property) {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newTemperaturePlan(metadata, expose.Property, exposeCanGet(expose))
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planNumericSensorProfileStrategy plans one generic read-only numeric
// sensor for a numeric root expose using only the compiled profile
// parameters: no mapping-specific Go branch is consulted. The upstream unit
// must match accepted_units exactly with no conversion; bounds are fixed or
// upstream-or-fallback; integer format decodes exact integers while float
// format preserves finite fractions. Publish access is required, set access
// is forbidden, and get access alone controls startup refresh.
func planNumericSensorProfileStrategy(input profileStrategyInput, compiled any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	parameters, ok := compiled.(numericSensorStrategyParameters)
	if !ok {
		return entityPlan{}, false
	}
	if !input.Root.resolved || input.Root.expose.Type != upstreamExposeNumeric {
		return entityPlan{}, false
	}
	expose := input.Root.expose
	if expose.Property == "" || !exposeCanPublish(expose) || exposeCanSet(expose) ||
		!input.Index.PropertyUnique(expose.Property) {
		return entityPlan{}, false
	}
	if !profileStrategyUnitAccepted(parameters.AcceptedUnits, expose.Unit) {
		return entityPlan{}, false
	}
	minimum, maximum, valid := resolveNumericSensorProfileBounds(expose, parameters.Bounds)
	if !valid {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newNumericSensorProfilePlan(
		metadata, expose.Property, minimum, maximum, parameters.Unit,
		parameters.NumberFormat, exposeCanGet(expose),
	)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planNumericSettingProfileStrategy plans one generic observable
// numeric-setting for a numeric root expose using only the compiled profile
// parameters: no mapping-specific Go branch is consulted. The strategy
// always requires publish, set, and get access, a device-unique property,
// the exact accepted unit, and both present finite upstream bounds with
// minimum below maximum. Fractions are preserved in numeric value mode with
// observed exact matching.
func planNumericSettingProfileStrategy(input profileStrategyInput, compiled any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	parameters, ok := compiled.(numericSettingStrategyParameters)
	if !ok {
		return entityPlan{}, false
	}
	if !input.Root.resolved || input.Root.expose.Type != upstreamExposeNumeric {
		return entityPlan{}, false
	}
	expose := input.Root.expose
	if expose.Property == "" || !exposeCanPublish(expose) || !exposeCanSet(expose) ||
		!exposeCanGet(expose) || !input.Index.PropertyUnique(expose.Property) {
		return entityPlan{}, false
	}
	if !profileStrategyUnitAccepted(parameters.AcceptedUnits, expose.Unit) {
		return entityPlan{}, false
	}
	minimum, maximum, valid := numericSettingBounds(expose)
	if !valid {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newNumericSettingPlan(metadata, expose.Property, minimum, maximum, parameters.Unit)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planEnumSettingProfileStrategy plans one observable enum-setting for an
// enum root expose. Choices come from the expose values and are never
// hard-coded; the strategy always requires publish, set, and get access
// with observed exact matching.
func planEnumSettingProfileStrategy(input profileStrategyInput, _ any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	if !input.Root.resolved || input.Root.expose.Type != upstreamExposeEnum {
		return entityPlan{}, false
	}
	expose := input.Root.expose
	if expose.Property == "" || !exposeCanPublish(expose) || !exposeCanSet(expose) ||
		!exposeCanGet(expose) || !input.Index.PropertyUnique(expose.Property) {
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newEnumSettingPlan(metadata, expose.Property, expose.Values)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// planEnumActionProfileStrategy plans one stateless dispatched enum-action
// for an enum root expose using only the compiled access parameter: no
// mapping-specific Go branch is consulted. Set-only requires access to equal
// the set bit; includes-set requires the set bit and ignores additional
// publish/get bits. The plan claims no state, runs no decoder, requests no
// refresh, and completes on adapter acceptance.
func planEnumActionProfileStrategy(input profileStrategyInput, compiled any) (entityPlan, bool) {
	if !input.Root.resolved {
		return entityPlan{}, false
	}
	parameters, ok := compiled.(enumActionStrategyParameters)
	if !ok {
		return entityPlan{}, false
	}
	if !input.Root.resolved || input.Root.expose.Type != upstreamExposeEnum {
		return entityPlan{}, false
	}
	expose := input.Root.expose
	if expose.Property == "" || !input.Index.PropertyUnique(expose.Property) {
		return entityPlan{}, false
	}
	switch parameters.Access {
	case profileEnumActionAccessSetOnly:
		if expose.Access != exposeSetAccessBit {
			return entityPlan{}, false
		}
	case profileEnumActionAccessIncludesSet:
		if !exposeCanSet(expose) {
			return entityPlan{}, false
		}
	default:
		return entityPlan{}, false
	}
	metadata, ok := profileStrategyEntityMetadata(input)
	if !ok {
		return entityPlan{}, false
	}
	plan, err := newEnumActionPlan(metadata, expose.Property, expose.Values)
	if err != nil {
		return entityPlan{}, false
	}
	return plan, true
}

// profileStrategyUnitAccepted reports whether one upstream unit matches the
// compiled accepted set exactly. "" in the set requires the upstream unit to
// be absent; no implicit conversion ever occurs.
func profileStrategyUnitAccepted(accepted []string, unit string) bool {
	return slices.Contains(accepted, unit)
}

// resolveNumericSensorProfileBounds resolves the support bounds for one
// generic numeric sensor. Fixed mode always uses the configured finite
// bounds and ignores upstream bound metadata. Upstream-or-fallback prefers
// both present valid finite upstream bounds, uses the configured values only
// when both upstream bounds are genuinely absent, and makes the Entity
// ineligible for malformed, one-sided, non-finite, or inverted upstream
// bounds.
func resolveNumericSensorProfileBounds(
	expose upstreamExpose,
	bounds numericSensorStrategyBounds,
) (float64, float64, bool) {
	if bounds.Mode == profileNumericSensorBoundsFixed {
		return bounds.Minimum, bounds.Maximum, true
	}
	if expose.ValueMin == nil && expose.ValueMax == nil {
		minimumAbsent := len(bytes.TrimSpace(expose.valueMinRaw)) == 0
		maximumAbsent := len(bytes.TrimSpace(expose.valueMaxRaw)) == 0
		if minimumAbsent && maximumAbsent {
			return bounds.Minimum, bounds.Maximum, true
		}
		return 0, 0, false
	}
	if expose.ValueMin == nil || expose.ValueMax == nil {
		return 0, 0, false
	}
	minimum, maximum := *expose.ValueMin, *expose.ValueMax
	if !isFinite(minimum) || !isFinite(maximum) || minimum >= maximum {
		return 0, 0, false
	}
	return minimum, maximum, true
}
