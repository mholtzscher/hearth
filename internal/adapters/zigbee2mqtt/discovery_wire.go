package zigbee2mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strconv"
)

const (
	upstreamOnline  = "online"
	upstreamOffline = "offline"
)

// bridgeInfo is the validated bridge information needed to prove compatible global behavior.
type bridgeInfo struct {
	Version             string
	MQTTVersion         int
	AvailabilityEnabled bool
	Optimistic          bool
}

// bridgeState is the retained bridge/state payload.
type bridgeState struct {
	State string `json:"state"`
}

// upstreamDevice and its nested DTOs are the private bridge/devices wire model.
type upstreamDevice struct {
	IEEEAddress    string                      `json:"ieee_address"`
	Type           string                      `json:"type"`
	Supported      bool                        `json:"supported"`
	Disabled       bool                        `json:"disabled"`
	FriendlyName   string                      `json:"friendly_name"`
	Description    string                      `json:"description"`
	InterviewState string                      `json:"interview_state"`
	Endpoints      map[string]upstreamEndpoint `json:"endpoints"`
	Definition     *upstreamDefinition         `json:"definition"`
}

type upstreamEndpoint struct {
	Name string `json:"name"`
}

type upstreamDefinition struct {
	Model       string           `json:"model"`
	Vendor      string           `json:"vendor"`
	Description string           `json:"description"`
	Exposes     []upstreamExpose `json:"exposes"`
}

type upstreamExpose struct {
	Type        string           `json:"type"`
	Name        string           `json:"name"`
	Property    string           `json:"property"`
	Endpoint    string           `json:"endpoint"`
	Access      int              `json:"access"`
	Unit        string           `json:"unit"`
	ValueOn     json.RawMessage  `json:"value_on"`
	ValueOff    json.RawMessage  `json:"value_off"`
	ValueMin    *float64         `json:"value_min"`
	ValueMax    *float64         `json:"value_max"`
	ValueStep   *float64         `json:"value_step"`
	Values      []string         `json:"values,omitempty"`
	Presets     []upstreamPreset `json:"presets,omitempty"`
	Features    []upstreamExpose `json:"features"`
	valueMinRaw json.RawMessage
	valueMaxRaw json.RawMessage
	// previousInvalid marks a previous-named preset whose value is missing,
	// non-integral, or anything other than the 65535 sentinel. It forces
	// omission of the dependent setting even though Presets holds only the
	// fully valid entries.
	previousInvalid bool
}

// upstreamPreset is one valid Zigbee2MQTT preset entry: a string name with
// an exact-integer value. Non-integral values are rejected at parse time and
// never reach planning; in-range numeric presets are wire aliases, not
// advertised Hearth choices.
type upstreamPreset struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

// UnmarshalJSON isolates malformed expose metadata so one optional feature cannot suppress valid siblings.
func (expose *upstreamExpose) UnmarshalJSON(payload []byte) error {
	*expose = upstreamExpose{}
	var wire struct {
		Type      json.RawMessage `json:"type"`
		Name      json.RawMessage `json:"name"`
		Property  json.RawMessage `json:"property"`
		Endpoint  json.RawMessage `json:"endpoint"`
		Access    json.RawMessage `json:"access"`
		Unit      json.RawMessage `json:"unit"`
		ValueOn   json.RawMessage `json:"value_on"`
		ValueOff  json.RawMessage `json:"value_off"`
		ValueMin  json.RawMessage `json:"value_min"`
		ValueMax  json.RawMessage `json:"value_max"`
		ValueStep json.RawMessage `json:"value_step"`
		Values    json.RawMessage `json:"values"`
		Presets   json.RawMessage `json:"presets"`
		Features  json.RawMessage `json:"features"`
	}
	_ = json.Unmarshal(payload, &wire)
	_ = json.Unmarshal(wire.Type, &expose.Type)
	_ = json.Unmarshal(wire.Name, &expose.Name)
	_ = json.Unmarshal(wire.Property, &expose.Property)
	_ = json.Unmarshal(wire.Endpoint, &expose.Endpoint)
	_ = json.Unmarshal(wire.Access, &expose.Access)
	_ = json.Unmarshal(wire.Unit, &expose.Unit)
	expose.ValueOn = bytes.Clone(wire.ValueOn)
	expose.ValueOff = bytes.Clone(wire.ValueOff)
	expose.ValueMin = decodeOptionalFloat(wire.ValueMin)
	expose.ValueMax = decodeOptionalFloat(wire.ValueMax)
	expose.ValueStep = decodeOptionalFloat(wire.ValueStep)
	expose.Values = decodeExposeValues(wire.Values)
	expose.Presets, expose.previousInvalid = decodeExposePresets(wire.Presets)
	expose.valueMinRaw = bytes.Clone(wire.ValueMin)
	expose.valueMaxRaw = bytes.Clone(wire.ValueMax)
	_ = json.Unmarshal(wire.Features, &expose.Features)
	return nil
}

// decodeExposeValues parses one enum values array tolerantly: any shape
// other than a JSON string array yields nil, which makes the dependent
// expose ineligible without affecting valid siblings.
func decodeExposeValues(payload json.RawMessage) []string {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	var values []string
	if decodeJSON(payload, &values) != nil {
		return nil
	}
	return values
}

// decodeExposePresets parses one presets array tolerantly. Entries with a
// string name and an exact-integer value are kept; unparseable entries and
// non-integral values are dropped, except that any previous-named entry
// failing the exact 65535 sentinel marks the whole preset set invalid so the
// dependent setting is omitted rather than advertised without its choice.
func decodeExposePresets(payload json.RawMessage) ([]upstreamPreset, bool) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, false
	}
	var entries []json.RawMessage
	if decodeJSON(payload, &entries) != nil {
		return nil, false
	}
	var presets []upstreamPreset
	invalidPrevious := false
	for _, entry := range entries {
		var probe struct {
			Name  json.RawMessage `json:"name"`
			Value json.RawMessage `json:"value"`
		}
		if decodeJSON(entry, &probe) != nil {
			continue
		}
		var name string
		if decodeJSON(probe.Name, &name) != nil || name == "" {
			continue
		}
		value, exact := exactPresetValue(probe.Value)
		if name != upstreamPreviousPreset {
			if exact {
				presets = append(presets, upstreamPreset{Name: name, Value: value})
			}
			continue
		}
		if !exact || value != startupPreviousWireValue {
			invalidPrevious = true
			continue
		}
		presets = append(presets, upstreamPreset{Name: name, Value: value})
	}
	return presets, invalidPrevious
}

// exactPresetValue decodes one exact-integer JSON number. Fractions,
// strings, and out-of-int64 values are rejected rather than rounded.
func exactPresetValue(payload json.RawMessage) (int64, bool) {
	value, err := parseExactIntegerJSON(payload)
	if err != nil {
		return 0, false
	}
	return value, true
}

func decodeOptionalFloat(payload json.RawMessage) *float64 {
	var decoded any
	if len(payload) == 0 || decodeJSON(payload, &decoded) != nil {
		return nil
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return nil
	}
	value, err := number.Float64()
	if err != nil {
		return nil
	}
	return &value
}

// scalarValue retains the upstream command representation and a semantic comparison key.
type scalarValue struct {
	Raw       json.RawMessage
	canonical string
}

func decodeBridgeInfo(payload []byte) (bridgeInfo, error) {
	var wire struct {
		Version *string `json:"version"`
		Config  *struct {
			MQTT *struct {
				Version *int `json:"version"`
			} `json:"mqtt"`
			Availability *struct {
				Enabled *bool `json:"enabled"`
			} `json:"availability"`
			DeviceOptions *struct {
				Optimistic *bool `json:"optimistic"`
			} `json:"device_options"`
		} `json:"config"`
	}
	if err := decodeJSON(payload, &wire); err != nil {
		return bridgeInfo{}, err
	}
	if wire.Version == nil || wire.Config == nil || wire.Config.MQTT == nil || wire.Config.MQTT.Version == nil ||
		wire.Config.Availability == nil || wire.Config.Availability.Enabled == nil ||
		wire.Config.DeviceOptions == nil || wire.Config.DeviceOptions.Optimistic == nil {
		return bridgeInfo{}, errors.New("bridge info is missing required fields")
	}
	if *wire.Version == "" {
		return bridgeInfo{}, errors.New("bridge info version is required")
	}
	return bridgeInfo{
		Version:             *wire.Version,
		MQTTVersion:         *wire.Config.MQTT.Version,
		AvailabilityEnabled: *wire.Config.Availability.Enabled,
		Optimistic:          *wire.Config.DeviceOptions.Optimistic,
	}, nil
}

func decodeRawArray(payload []byte) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("inventory must be a JSON array")
	}
	var items []json.RawMessage
	if err := decodeJSON(payload, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func decodeUpstreamDevice(payload []byte) (upstreamDevice, error) {
	if trimmed := bytes.TrimSpace(payload); len(trimmed) == 0 || trimmed[0] != '{' {
		return upstreamDevice{}, errors.New("device must be a JSON object")
	}
	var device upstreamDevice
	if err := decodeJSON(payload, &device); err != nil {
		return upstreamDevice{}, err
	}
	return device, nil
}

func decodeJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func diagnosticRejection(payload []byte) deviceRejection {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(payload, &fields)
	var ieeeAddress string
	_ = json.Unmarshal(fields["ieee_address"], &ieeeAddress)
	normalized, _ := normalizeIEEEAddress(ieeeAddress)
	var definition struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(fields["definition"], &definition)
	return deviceRejection{IEEEAddress: normalized, Model: definition.Model, Code: rejectionMalformedDevice}
}

func canonicalScalar(payload json.RawMessage) (scalarValue, error) {
	var value any
	if err := decodeJSON(payload, &value); err != nil {
		return scalarValue{}, err
	}
	var canonical string
	switch typed := value.(type) {
	case nil:
		canonical = "null"
	case bool:
		canonical = "bool:" + strconv.FormatBool(typed)
	case string:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return scalarValue{}, err
		}
		canonical = "string:" + string(encoded)
	case json.Number:
		number, ok := new(big.Rat).SetString(typed.String())
		if !ok {
			return scalarValue{}, errors.New("invalid JSON number")
		}
		canonical = "number:" + number.RatString()
	default:
		return scalarValue{}, errors.New("Zigbee2MQTT value must be a JSON scalar")
	}
	return scalarValue{Raw: bytes.Clone(bytes.TrimSpace(payload)), canonical: canonical}, nil
}
