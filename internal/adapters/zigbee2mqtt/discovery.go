package zigbee2mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	requiredAccessMask       = 7
	ieeeAddressLength        = 18
	hearthBrightnessMaximum  = 100
	maximumRouteSlugBytes    = 63
	maximumEntitiesPerDevice = 64
	maximumDescriptorRunes   = 128
	upstreamDeviceKindLight  = "light"
	upstreamBrightnessName   = "brightness"
	upstreamOnline           = "online"
	upstreamOffline          = "offline"

	rejectionMalformedDevice   = "malformed_device"
	rejectionCoordinator       = "coordinator"
	rejectionUnsupported       = "unsupported"
	rejectionDisabled          = "disabled"
	rejectionInterview         = "interview_incomplete"
	rejectionMissingDefinition = "missing_definition"
	rejectionInvalidIEEE       = "invalid_ieee_address"
	rejectionInvalidName       = "invalid_friendly_name"
	rejectionNoEligibleLight   = "no_eligible_light"
	rejectionTooManyEntities   = "too_many_entities"
	rejectionInvalidDescriptor = "invalid_descriptor"
	rejectionDuplicateIEEE     = "duplicate_ieee_address"
	rejectionDuplicateFriendly = "duplicate_friendly_name"
)

// bridgeInfo is the retained bridge/info payload needed to prove compatible global behavior.
type bridgeInfo struct {
	Version string `json:"version"`
	Config  struct {
		MQTT struct {
			Version int `json:"version"`
		} `json:"mqtt"`
		Availability struct {
			Enabled bool `json:"enabled"`
		} `json:"availability"`
		DeviceOptions struct {
			Optimistic *bool `json:"optimistic"`
		} `json:"device_options"`
	} `json:"config"`
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
	Type      string           `json:"type"`
	Name      string           `json:"name"`
	Property  string           `json:"property"`
	Endpoint  string           `json:"endpoint"`
	Access    int              `json:"access"`
	ValueOn   json.RawMessage  `json:"value_on"`
	ValueOff  json.RawMessage  `json:"value_off"`
	ValueMin  *float64         `json:"value_min"`
	ValueMax  *float64         `json:"value_max"`
	ValueStep *float64         `json:"value_step"`
	Features  []upstreamExpose `json:"features"`
}

type entityKind uint8

const (
	entityKindPower entityKind = iota + 1
	entityKindBrightness
)

// inventoryDiscovery is the complete, deterministic result consumed by adapter reconciliation.
type inventoryDiscovery struct {
	Devices    []discoveredDevice
	Rejections []deviceRejection
}

// discoveredDevice contains one registration and the MQTT routes that registration establishes.
type discoveredDevice struct {
	IEEEAddress  string
	FriendlyName string
	Model        string
	Registration adapter.Registration
	Entities     []discoveredEntity
}

// discoveredEntity joins a registration Entity key to its state and command wire metadata.
type discoveredEntity struct {
	Descriptor        adapter.EntityDescriptor
	Property          string
	Kind              entityKind
	Endpoint          int
	Scoped            bool
	PowerOn           scalarValue
	PowerOff          scalarValue
	BrightnessMaximum float64
}

// deviceRejection gives adapter diagnostics and reconciliation a safe identity and stable reason.
type deviceRejection struct {
	IEEEAddress string
	Model       string
	Code        string
}

// scalarValue retains the upstream command representation and a semantic comparison key.
type scalarValue struct {
	Raw       json.RawMessage
	canonical string
}

type exposeCandidate struct {
	powerKey string
	entities []discoveredEntity
	invalid  bool
}

// discoverInventory parses one complete bridge/devices document, isolating malformed array elements.
func discoverInventory(payload []byte) (inventoryDiscovery, error) {
	items, err := decodeRawArray(payload)
	if err != nil {
		return inventoryDiscovery{}, fmt.Errorf("decode Zigbee2MQTT device inventory: %w", err)
	}
	result := inventoryDiscovery{
		Devices:    make([]discoveredDevice, 0, len(items)),
		Rejections: make([]deviceRejection, 0),
	}
	for _, item := range items {
		device, decodeErr := decodeUpstreamDevice(item)
		if decodeErr != nil {
			result.Rejections = append(result.Rejections, diagnosticRejection(item))
			continue
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			if rejection.Code != rejectionCoordinator {
				result.Rejections = append(result.Rejections, *rejection)
			}
			continue
		}
		result.Devices = append(result.Devices, discovered)
	}
	isolateDuplicateIEEE(&result)
	isolateDuplicateFriendlyNames(&result)
	return result, nil
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

func discoverDevice(device upstreamDevice) (discoveredDevice, *deviceRejection) {
	normalizedIEEE, ieeeErr := normalizeIEEEAddress(device.IEEEAddress)
	rejection := deviceRejection{Model: definitionModel(device.Definition)}
	if ieeeErr == nil {
		rejection.IEEEAddress = normalizedIEEE
	}
	switch {
	case device.Type == "Coordinator":
		rejection.Code = rejectionCoordinator
	case device.Type == "Group":
		rejection.Code = rejectionUnsupported
	case device.Disabled:
		rejection.Code = rejectionDisabled
	case !device.Supported:
		rejection.Code = rejectionUnsupported
	case device.InterviewState != "SUCCESSFUL":
		rejection.Code = rejectionInterview
	case device.Definition == nil:
		rejection.Code = rejectionMissingDefinition
	case ieeeErr != nil:
		rejection.Code = rejectionInvalidIEEE
	case !validRouteSlug(device.FriendlyName):
		rejection.Code = rejectionInvalidName
	default:
		return buildDiscoveredDevice(device, normalizedIEEE)
	}
	return discoveredDevice{}, &rejection
}

func definitionModel(definition *upstreamDefinition) string {
	if definition == nil {
		return ""
	}
	return definition.Model
}

func buildDiscoveredDevice(device upstreamDevice, ieeeAddress string) (discoveredDevice, *deviceRejection) {
	name := device.FriendlyName
	if description := strings.TrimSpace(device.Description); description != "" {
		name = description
	}
	if utf8.RuneCountInString(name) > maximumDescriptorRunes {
		return rejectedDevice(device, ieeeAddress, rejectionInvalidDescriptor)
	}
	candidates := collectExposeCandidates(device)
	entities := selectUnambiguousEntities(candidates)
	if len(entities) == 0 {
		return rejectedDevice(device, ieeeAddress, rejectionNoEligibleLight)
	}
	if len(entities) > maximumEntitiesPerDevice {
		return rejectedDevice(device, ieeeAddress, rejectionTooManyEntities)
	}

	descriptors := make([]adapter.EntityDescriptor, 0, len(entities))
	for index := range entities {
		descriptor, err := makeEntityDescriptor(ieeeAddress, entities[index])
		if err != nil {
			return rejectedDevice(device, ieeeAddress, rejectionInvalidDescriptor)
		}
		entities[index].Descriptor = descriptor
		descriptors = append(descriptors, descriptor)
	}
	externalID := ieeeAddress
	return discoveredDevice{
		IEEEAddress:  ieeeAddress,
		FriendlyName: device.FriendlyName,
		Model:        device.Definition.Model,
		Registration: adapter.Registration{
			BindingKey: "z2m-" + strings.TrimPrefix(ieeeAddress, "0x"),
			Device: adapter.DeviceDescriptor{
				ExternalID: &externalID,
				Name:       name,
				Kind:       upstreamDeviceKindLight,
			},
			Entities: descriptors,
		},
		Entities: entities,
	}, nil
}

func rejectedDevice(device upstreamDevice, ieeeAddress, code string) (discoveredDevice, *deviceRejection) {
	return discoveredDevice{}, &deviceRejection{
		IEEEAddress: ieeeAddress,
		Model:       definitionModel(device.Definition),
		Code:        code,
	}
}

func collectExposeCandidates(device upstreamDevice) []exposeCandidate {
	propertyCounts := relevantPropertyCounts(device.Definition.Exposes)
	candidates := make([]exposeCandidate, 0)
	for _, expose := range device.Definition.Exposes {
		candidate, ok := makeExposeCandidate(device, expose, propertyCounts)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func relevantPropertyCounts(exposes []upstreamExpose) map[string]int {
	counts := make(map[string]int)
	var countProperties func([]upstreamExpose)
	countProperties = func(items []upstreamExpose) {
		for _, expose := range items {
			if expose.Property != "" {
				counts[expose.Property]++
			}
			countProperties(expose.Features)
		}
	}
	countProperties(exposes)
	return counts
}

func makeExposeCandidate(
	device upstreamDevice,
	expose upstreamExpose,
	propertyCounts map[string]int,
) (exposeCandidate, bool) {
	if expose.Type != upstreamDeviceKindLight {
		return exposeCandidate{}, false
	}
	endpoint, scoped, resolved := resolveEndpoint(expose.Endpoint, device.Endpoints)
	if !resolved {
		return exposeCandidate{}, false
	}
	powerFeature, ok := uniqueFeature(expose.Features, "binary", "state")
	if !ok || !validPowerFeature(powerFeature) || propertyCounts[powerFeature.Property] != 1 {
		return exposeCandidate{}, false
	}
	powerOn, onErr := canonicalScalar(powerFeature.ValueOn)
	powerOff, offErr := canonicalScalar(powerFeature.ValueOff)
	if onErr != nil || offErr != nil || powerOn.canonical == powerOff.canonical {
		return exposeCandidate{}, false
	}
	powerKey, powerName := entityIdentity(entityKindPower, expose.Endpoint, endpoint, scoped)
	if utf8.RuneCountInString(powerName) > maximumDescriptorRunes {
		return exposeCandidate{}, false
	}
	power := discoveredEntity{
		Property: powerFeature.Property, Kind: entityKindPower, Endpoint: endpoint, Scoped: scoped,
		PowerOn: powerOn, PowerOff: powerOff,
	}
	power.Descriptor.Key = powerKey
	power.Descriptor.Name = powerName
	candidate := exposeCandidate{powerKey: powerKey, entities: []discoveredEntity{power}}

	brightnessFeature, brightnessOK := uniqueFeature(expose.Features, "numeric", upstreamBrightnessName)
	if brightnessOK && validBrightnessFeature(brightnessFeature) && propertyCounts[brightnessFeature.Property] == 1 {
		key, name := entityIdentity(entityKindBrightness, expose.Endpoint, endpoint, scoped)
		if utf8.RuneCountInString(name) > maximumDescriptorRunes {
			return candidate, true
		}
		brightness := discoveredEntity{
			Property: brightnessFeature.Property, Kind: entityKindBrightness,
			Endpoint: endpoint, Scoped: scoped, BrightnessMaximum: *brightnessFeature.ValueMax,
		}
		brightness.Descriptor.Key = key
		brightness.Descriptor.Name = name
		candidate.entities = append(candidate.entities, brightness)
	}
	return candidate, true
}

func uniqueFeature(features []upstreamExpose, featureType, name string) (upstreamExpose, bool) {
	var found upstreamExpose
	matches := 0
	for _, feature := range features {
		if feature.Type == featureType && feature.Name == name {
			found = feature
			matches++
		}
	}
	return found, matches == 1
}

func validPowerFeature(feature upstreamExpose) bool {
	return feature.Access&requiredAccessMask == requiredAccessMask && feature.Property != "" &&
		len(feature.ValueOn) != 0 && len(feature.ValueOff) != 0
}

func validBrightnessFeature(feature upstreamExpose) bool {
	return feature.Access&requiredAccessMask == requiredAccessMask && feature.Property != "" &&
		feature.ValueMin != nil && *feature.ValueMin == 0 &&
		feature.ValueMax != nil && brightnessRangeSupported(*feature.ValueMax)
}

func selectUnambiguousEntities(candidates []exposeCandidate) []discoveredEntity {
	powerKeys := make(map[string]int)
	for _, candidate := range candidates {
		powerKeys[candidate.powerKey]++
	}
	entityKeys := make(map[string]int)
	for index := range candidates {
		if powerKeys[candidates[index].powerKey] != 1 {
			candidates[index].invalid = true
			continue
		}
		for _, entity := range candidates[index].entities {
			entityKeys[entity.Descriptor.Key]++
		}
	}
	entities := make([]discoveredEntity, 0)
	for _, candidate := range candidates {
		if candidate.invalid {
			continue
		}
		for _, entity := range candidate.entities {
			if entityKeys[entity.Descriptor.Key] == 1 {
				entities = append(entities, entity)
			}
		}
	}
	return entities
}

func resolveEndpoint(reference string, endpoints map[string]upstreamEndpoint) (int, bool, bool) {
	if reference == "" {
		return 0, false, true
	}
	matches := make(map[int]struct{})
	for key, endpoint := range endpoints {
		number, err := strconv.Atoi(key)
		if err != nil || number < 0 {
			continue
		}
		if key == reference || endpoint.Name == reference {
			matches[number] = struct{}{}
		}
	}
	if len(matches) != 1 {
		return 0, true, false
	}
	for number := range matches {
		return number, true, true
	}
	panic("unreachable endpoint resolution")
}

func entityIdentity(kind entityKind, label string, endpoint int, scoped bool) (string, string) {
	baseKey := "power"
	baseName := "Power"
	if kind == entityKindBrightness {
		baseKey = "brightness"
		baseName = "Brightness"
	}
	if !scoped {
		return baseKey, baseName
	}
	endpointText := strconv.Itoa(endpoint)
	nameLabel := strings.TrimSpace(label)
	if _, err := strconv.Atoi(nameLabel); nameLabel == "" || err == nil {
		nameLabel = "ep" + endpointText
	}
	return baseKey + "-ep" + endpointText, nameLabel + " " + baseName
}

func makeEntityDescriptor(ieeeAddress string, entity discoveredEntity) (adapter.EntityDescriptor, error) {
	if utf8.RuneCountInString(entity.Descriptor.Name) > maximumDescriptorRunes {
		return adapter.EntityDescriptor{}, errors.New("entity name exceeds 128 runes")
	}
	location := "root"
	if entity.Scoped {
		location = "ep" + strconv.Itoa(entity.Endpoint)
	}
	metadata := adapter.EntityMetadata{
		Key:        entity.Descriptor.Key,
		ExternalID: ieeeAddress + "/" + location + "/" + entityKindName(entity.Kind),
		Name:       entity.Descriptor.Name,
	}
	switch entity.Kind {
	case entityKindPower:
		return sdkpowerv1.NewEntityDescriptor(metadata, contractpowerv1.Support{
			State:      contractpowerv1.StateSupport{},
			Operations: contractpowerv1.OperationSupport{Set: contractpowerv1.SetSupport{}},
		})
	case entityKindBrightness:
		return sdkbrightnessv1.NewEntityDescriptor(metadata, contractbrightnessv1.Support{
			State: contractbrightnessv1.StateSupport{Maximum: hearthBrightnessMaximum},
			Operations: contractbrightnessv1.OperationSupport{
				Set: contractbrightnessv1.SetSupport{Step: 1},
			},
		})
	default:
		return adapter.EntityDescriptor{}, errors.New("unknown Entity kind")
	}
}

func entityKindName(kind entityKind) string {
	if kind == entityKindBrightness {
		return "brightness"
	}
	return "power"
}

func normalizeIEEEAddress(value string) (string, error) {
	if len(value) != ieeeAddressLength || value[0] != '0' || (value[1] != 'x' && value[1] != 'X') {
		return "", errors.New("IEEE address must have a 0x prefix and 16 hexadecimal digits")
	}
	for _, character := range value[2:] {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return "", errors.New("IEEE address contains a non-hexadecimal digit")
		}
	}
	return "0x" + strings.ToLower(value[2:]), nil
}

func validRouteSlug(value string) bool {
	if len(value) == 0 || len(value) > maximumRouteSlugBytes || !isLowerAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isLowerAlphaNumeric(value[index]) && value[index] != '_' && value[index] != '-' {
			return false
		}
	}
	return true
}

func isLowerAlphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
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

func brightnessRangeSupported(maximum float64) bool {
	if !isFinite(maximum) || maximum < hearthBrightnessMaximum {
		return false
	}
	for percentage := int64(0); percentage <= hearthBrightnessMaximum; percentage++ {
		scaled := float64(percentage) * maximum / hearthBrightnessMaximum
		normalized, err := normalizeBrightnessValue(scaled, maximum)
		if err != nil || normalized != percentage {
			return false
		}
	}
	return true
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isolateDuplicateFriendlyNames(result *inventoryDiscovery) {
	counts := make(map[string]int)
	for _, device := range result.Devices {
		counts[device.FriendlyName]++
	}
	unique := make([]discoveredDevice, 0, len(result.Devices))
	for _, device := range result.Devices {
		if counts[device.FriendlyName] == 1 {
			unique = append(unique, device)
			continue
		}
		result.Rejections = append(result.Rejections, deviceRejection{
			IEEEAddress: device.IEEEAddress,
			Model:       device.Model,
			Code:        rejectionDuplicateFriendly,
		})
	}
	result.Devices = unique
}

func isolateDuplicateIEEE(result *inventoryDiscovery) {
	counts := make(map[string]int)
	for _, device := range result.Devices {
		counts[device.IEEEAddress]++
	}
	unique := make([]discoveredDevice, 0, len(result.Devices))
	rejected := make(map[string]struct{})
	for _, device := range result.Devices {
		if counts[device.IEEEAddress] == 1 {
			unique = append(unique, device)
			continue
		}
		if _, seen := rejected[device.IEEEAddress]; !seen {
			result.Rejections = append(result.Rejections, deviceRejection{
				IEEEAddress: device.IEEEAddress,
				Model:       device.Model,
				Code:        rejectionDuplicateIEEE,
			})
			rejected[device.IEEEAddress] = struct{}{}
		}
	}
	result.Devices = unique
}
