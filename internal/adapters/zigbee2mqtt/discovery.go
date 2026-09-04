package zigbee2mqtt

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	ieeeAddressLength        = 18
	maximumRouteSlugBytes    = 63
	maximumEntitiesPerDevice = 64
	maximumDescriptorRunes   = 128
	upstreamDeviceKindLight  = "light"
	upstreamDeviceKindRelay  = "relay"
	upstreamDeviceKindSensor = "sensor"
	upstreamExposeSwitch     = "switch"

	rejectionMalformedDevice     = "malformed_device"
	rejectionCoordinator         = "coordinator"
	rejectionUnsupported         = "unsupported"
	rejectionDisabled            = "disabled"
	rejectionInterview           = "interview_incomplete"
	rejectionMissingDefinition   = "missing_definition"
	rejectionInvalidIEEE         = "invalid_ieee_address"
	rejectionInvalidName         = "invalid_friendly_name"
	rejectionNoEligibleLight     = "no_eligible_light"
	rejectionNoEligibleRelay     = "no_eligible_relay"
	rejectionNoEligibleEntity    = "no_eligible_entity"
	rejectionAmbiguousEntityPlan = "ambiguous_entity_plan"
	rejectionTooManyEntities     = "too_many_entities"
	rejectionInvalidDescriptor   = "invalid_descriptor"
	rejectionDuplicateIEEE       = "duplicate_ieee_address"
	rejectionDuplicateFriendly   = "duplicate_friendly_name"
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
	Entities     []entityPlan
}

// deviceRejection gives adapter diagnostics and reconciliation a safe identity and stable reason.
type deviceRejection struct {
	IEEEAddress string
	Model       string
	Code        string
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
	plan, err := planDevice(
		devicePlanningInput{IEEE: ieeeAddress, Exposes: newExposeIndex(device)},
		defaultDevicePlanners(),
	)
	if err != nil {
		code := rejectionInvalidDescriptor
		if planErr, ok := errors.AsType[*devicePlanError](err); ok {
			code = planErr.code
		}
		return rejectedDevice(device, ieeeAddress, code)
	}

	descriptors := make([]adapter.EntityDescriptor, 0, len(plan.Entities))
	for _, entity := range plan.Entities {
		descriptors = append(descriptors, entity.Descriptor)
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
				Kind:       plan.Kind,
			},
			Entities: descriptors,
		},
		Entities: plan.Entities,
	}, nil
}

func rejectedDevice(device upstreamDevice, ieeeAddress, code string) (discoveredDevice, *deviceRejection) {
	return discoveredDevice{}, &deviceRejection{
		IEEEAddress: ieeeAddress,
		Model:       definitionModel(device.Definition),
		Code:        code,
	}
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
