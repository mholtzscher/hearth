package zigbee2mqtt

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkcolortempv1 "github.com/mholtzscher/hearth/sdk/adapter/colortempv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	requiredAccessMask      = 7
	hearthBrightnessMaximum = 100
	hearthColorTempMinimum  = 100
	hearthColorTempMaximum  = 1000
	upstreamBrightnessName  = "brightness"
	upstreamColorTempName   = "color_temp"
)

type entityKind uint8

const (
	entityKindPower entityKind = iota + 1
	entityKindBrightness
	entityKindColorTemp
)

type exposeCandidate struct {
	powerKey string
	entities []discoveredEntity
	invalid  bool
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

	colorTempFeature, colorTempOK := uniqueFeature(expose.Features, "numeric", upstreamColorTempName)
	minimum, maximum, validRange := colorTempRange(colorTempFeature)
	if colorTempOK && validRange && propertyCounts[colorTempFeature.Property] == 1 {
		key, name := entityIdentity(entityKindColorTemp, expose.Endpoint, endpoint, scoped)
		if utf8.RuneCountInString(name) > maximumDescriptorRunes {
			return candidate, true
		}
		colorTemp := discoveredEntity{
			Property: colorTempFeature.Property, Kind: entityKindColorTemp,
			Endpoint: endpoint, Scoped: scoped, ColorTempMinimum: minimum, ColorTempMaximum: maximum,
		}
		colorTemp.Descriptor.Key = key
		colorTemp.Descriptor.Name = name
		candidate.entities = append(candidate.entities, colorTemp)
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

func colorTempRange(feature upstreamExpose) (int64, int64, bool) {
	if feature.Access&requiredAccessMask != requiredAccessMask || feature.Property == "" {
		return 0, 0, false
	}
	minimum, minimumOK := colorTempBound(feature.valueMinRaw, feature.ValueMin)
	maximum, maximumOK := colorTempBound(feature.valueMaxRaw, feature.ValueMax)
	if !minimumOK || !maximumOK || minimum < hearthColorTempMinimum || maximum > hearthColorTempMaximum ||
		minimum >= maximum {
		return 0, 0, false
	}
	return minimum, maximum, true
}

func colorTempBound(payload json.RawMessage, fallback *float64) (int64, bool) {
	if len(payload) == 0 {
		if fallback == nil || !isFinite(*fallback) || math.Trunc(*fallback) != *fallback ||
			*fallback < hearthColorTempMinimum || *fallback > hearthColorTempMaximum {
			return 0, false
		}
		return int64(*fallback), true
	}
	var decoded any
	if decodeJSON(payload, &decoded) != nil {
		return 0, false
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, false
	}
	exact, ok := new(big.Rat).SetString(number.String())
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, false
	}
	return exact.Num().Int64(), true
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
	var baseKey, baseName string
	switch kind {
	case entityKindPower:
		baseKey = "power"
		baseName = "Power"
	case entityKindBrightness:
		baseKey = "brightness"
		baseName = "Brightness"
	case entityKindColorTemp:
		baseKey = "colortemp"
		baseName = "Color Temperature"
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
		return sdkpowerv1.NewEntityDescriptor(metadata, powerSupport())
	case entityKindBrightness:
		return sdkbrightnessv1.NewEntityDescriptor(metadata, brightnessSupport())
	case entityKindColorTemp:
		return sdkcolortempv1.NewEntityDescriptor(metadata, colorTempSupport(entity))
	default:
		return adapter.EntityDescriptor{}, errors.New("unknown Entity kind")
	}
}

func entityKindName(kind entityKind) string {
	switch kind {
	case entityKindPower:
		return "power"
	case entityKindBrightness:
		return "brightness"
	case entityKindColorTemp:
		return "colortemp"
	default:
		return ""
	}
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
