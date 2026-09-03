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
	ValueOn     json.RawMessage  `json:"value_on"`
	ValueOff    json.RawMessage  `json:"value_off"`
	ValueMin    *float64         `json:"value_min"`
	ValueMax    *float64         `json:"value_max"`
	ValueStep   *float64         `json:"value_step"`
	Features    []upstreamExpose `json:"features"`
	valueMinRaw json.RawMessage
	valueMaxRaw json.RawMessage
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
		ValueOn   json.RawMessage `json:"value_on"`
		ValueOff  json.RawMessage `json:"value_off"`
		ValueMin  json.RawMessage `json:"value_min"`
		ValueMax  json.RawMessage `json:"value_max"`
		ValueStep json.RawMessage `json:"value_step"`
		Features  json.RawMessage `json:"features"`
	}
	_ = json.Unmarshal(payload, &wire)
	_ = json.Unmarshal(wire.Type, &expose.Type)
	_ = json.Unmarshal(wire.Name, &expose.Name)
	_ = json.Unmarshal(wire.Property, &expose.Property)
	_ = json.Unmarshal(wire.Endpoint, &expose.Endpoint)
	_ = json.Unmarshal(wire.Access, &expose.Access)
	expose.ValueOn = bytes.Clone(wire.ValueOn)
	expose.ValueOff = bytes.Clone(wire.ValueOff)
	expose.ValueMin = decodeOptionalFloat(wire.ValueMin)
	expose.ValueMax = decodeOptionalFloat(wire.ValueMax)
	expose.ValueStep = decodeOptionalFloat(wire.ValueStep)
	expose.valueMinRaw = bytes.Clone(wire.ValueMin)
	expose.valueMaxRaw = bytes.Clone(wire.ValueMax)
	_ = json.Unmarshal(wire.Features, &expose.Features)
	return nil
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
		return scalarValue{}, errors.New("power value must be a JSON scalar")
	}
	return scalarValue{Raw: bytes.Clone(bytes.TrimSpace(payload)), canonical: canonical}, nil
}
