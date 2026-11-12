package nativeapi

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

// Message type IDs this client handles. TestMessageIDsMatchSchema checks them against the schema's (id) options.
const (
	idHelloRequest             uint16 = 1
	idHelloResponse            uint16 = 2
	idDisconnectRequest        uint16 = 5
	idDisconnectResponse       uint16 = 6
	idPingRequest              uint16 = 7
	idPingResponse             uint16 = 8
	idDeviceInfoRequest        uint16 = 9
	idDeviceInfoResponse       uint16 = 10
	idListEntitiesRequest      uint16 = 11
	idListEntitiesDoneResponse uint16 = 19
	idSubscribeStatesRequest   uint16 = 20
	idBinarySensorState        uint16 = 21
	idLightState               uint16 = 24
	idSensorState              uint16 = 25
	idSwitchState              uint16 = 26
	idLightCommandRequest      uint16 = 32
	idSwitchCommandRequest     uint16 = 33
)

// schema indexes every message in the vendored schema by its (id) option.
type schema struct {
	byID   map[uint16]protoreflect.MessageType
	byName map[protoreflect.FullName]uint16
	// listEntities holds the IDs of per-type ListEntities responses, excluding the done marker.
	listEntities map[uint16]string
}

//nolint:gochecknoglobals // The immutable schema index is built once from protobuf descriptors.
var apiSchema = mustLoadSchema()

func mustLoadSchema() schema {
	loaded, err := loadSchema()
	if err != nil {
		panic(err)
	}
	return loaded
}

func loadSchema() (schema, error) {
	loaded := schema{
		byID:         map[uint16]protoreflect.MessageType{},
		byName:       map[protoreflect.FullName]uint16{},
		listEntities: map[uint16]string{},
	}
	messages := apipb.File_api_proto.Messages()
	for index := range messages.Len() {
		descriptor := messages.Get(index)
		id, ok := proto.GetExtension(descriptor.Options(), apipb.E_Id).(uint32)
		if !ok || id == 0 {
			continue
		}
		if id > math.MaxUint16 {
			return schema{}, fmt.Errorf("message %s has out-of-range id %d", descriptor.FullName(), id)
		}
		messageType, err := protoregistry.GlobalTypes.FindMessageByName(descriptor.FullName())
		if err != nil {
			return schema{}, fmt.Errorf("find message %s: %w", descriptor.FullName(), err)
		}
		messageID := uint16(id)
		loaded.byID[messageID] = messageType
		loaded.byName[descriptor.FullName()] = messageID
		name := string(descriptor.Name())
		if platform, isList := strings.CutPrefix(
			name,
			"ListEntities",
		); isList &&
			messageID != idListEntitiesDoneResponse {
			if responsePlatform, isResponse := strings.CutSuffix(
				platform,
				"Response",
			); isResponse &&
				responsePlatform != "" {
				loaded.listEntities[messageID] = snakeCase(responsePlatform)
			}
		}
	}
	return loaded, nil
}

// encode returns a message's type ID and wire bytes.
func encode(message proto.Message) (uint16, []byte, error) {
	id, ok := apiSchema.byName[message.ProtoReflect().Descriptor().FullName()]
	if !ok {
		return 0, nil, fmt.Errorf("message %s has no type id", message.ProtoReflect().Descriptor().FullName())
	}
	data, err := proto.Marshal(message)
	if err != nil {
		return 0, nil, fmt.Errorf("encode %s: %w", message.ProtoReflect().Descriptor().Name(), err)
	}
	return id, data, nil
}

// decodeInto unmarshals wire bytes into target, keeping unknown fields out of the decoded values.
func decodeInto(data []byte, target proto.Message) error {
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", target.ProtoReflect().Descriptor().Name(), err)
	}
	return nil
}

func decodeDeviceInfo(data []byte) (DeviceInfo, error) {
	var message apipb.DeviceInfoResponse
	if err := decodeInto(data, &message); err != nil {
		return DeviceInfo{}, err
	}
	return DeviceInfo{
		MAC:            message.GetMacAddress(),
		Name:           message.GetName(),
		FriendlyName:   message.GetFriendlyName(),
		ESPHomeVersion: message.GetEsphomeVersion(),
		Model:          message.GetModel(),
		Manufacturer:   message.GetManufacturer(),
		ProjectName:    message.GetProjectName(),
		ProjectVersion: message.GetProjectVersion(),
		HasDeepSleep:   message.GetHasDeepSleep(),
	}, nil
}

// decodeEntity decodes any per-type ListEntities response. Common fields are read by name so every platform yields its
// identity; only the mapped platforms decode their type-specific fields.
func decodeEntity(messageID uint16, data []byte) (EntityInfo, error) {
	platform, ok := apiSchema.listEntities[messageID]
	if !ok {
		return EntityInfo{}, errors.New("esphome: not a list-entities response")
	}
	message := apiSchema.byID[messageID].New().Interface()
	if err := decodeInto(data, message); err != nil {
		return EntityInfo{}, err
	}
	reflected := message.ProtoReflect()
	info := EntityInfo{
		Type:              EntityOther,
		UpstreamType:      platform,
		Key:               uint32Field(reflected, "key"),
		DeviceID:          uint32Field(reflected, "device_id"),
		Name:              stringField(reflected, "name"),
		DisabledByDefault: boolField(reflected, "disabled_by_default"),
		DeviceClass:       stringField(reflected, "device_class"),
	}
	if field := reflected.Descriptor().Fields().ByName("entity_category"); field != nil {
		info.Category = EntityCategory(reflected.Get(field).Enum()) //nolint:gosec // Category values are 0 to 2.
	}
	switch typed := message.(type) {
	case *apipb.ListEntitiesSensorResponse:
		info.Type = EntitySensor
		info.Unit = typed.GetUnitOfMeasurement()
		info.AccuracyDecimals = typed.GetAccuracyDecimals()
	case *apipb.ListEntitiesBinarySensorResponse:
		info.Type = EntityBinarySensor
	case *apipb.ListEntitiesSwitchResponse:
		info.Type = EntitySwitch
		info.AssumedState = typed.GetAssumedState()
	case *apipb.ListEntitiesLightResponse:
		info.Type = EntityLight
		for _, mode := range typed.GetSupportedColorModes() {
			info.ColorModes = append(info.ColorModes, uint32(mode)) //nolint:gosec // Color modes are small positives.
		}
	}
	return info, nil
}

// decodeState decodes a mapped platform's state push. ok is false for every other message.
func decodeState(messageID uint16, data []byte) (StateUpdate, bool, error) {
	switch messageID {
	case idSensorState:
		var message apipb.SensorStateResponse
		if err := decodeInto(data, &message); err != nil {
			return StateUpdate{}, false, err
		}
		return StateUpdate{
			Type: EntitySensor, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
			Missing: message.GetMissingState(), Number: shortestFloat(message.GetState()),
		}, true, nil
	case idBinarySensorState:
		var message apipb.BinarySensorStateResponse
		if err := decodeInto(data, &message); err != nil {
			return StateUpdate{}, false, err
		}
		return StateUpdate{
			Type: EntityBinarySensor, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
			Missing: message.GetMissingState(), Bool: message.GetState(),
		}, true, nil
	case idSwitchState:
		var message apipb.SwitchStateResponse
		if err := decodeInto(data, &message); err != nil {
			return StateUpdate{}, false, err
		}
		return StateUpdate{
			Type: EntitySwitch, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
			Missing: message.GetMissingState(), Bool: message.GetState(),
		}, true, nil
	case idLightState:
		var message apipb.LightStateResponse
		if err := decodeInto(data, &message); err != nil {
			return StateUpdate{}, false, err
		}
		return StateUpdate{
			Type: EntityLight, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
			Bool: message.GetState(), Brightness: shortestFloat(message.GetBrightness()),
		}, true, nil
	default:
		return StateUpdate{}, false, nil
	}
}

// commandMessage converts a Command into its request message.
func commandMessage(command Command) (proto.Message, error) {
	switch typed := command.(type) {
	case SwitchCommand:
		return &apipb.SwitchCommandRequest{Key: typed.Key, DeviceId: typed.DeviceID, State: typed.On}, nil
	case LightCommand:
		request := &apipb.LightCommandRequest{Key: typed.Key, DeviceId: typed.DeviceID, HasState: true, State: typed.On}
		if typed.Brightness != nil {
			request.HasBrightness = true
			request.Brightness = float32(*typed.Brightness)
		}
		return request, nil
	default:
		return nil, fmt.Errorf("esphome: unsupported command %T", command)
	}
}

// shortestFloat widens a wire float32 to the float64 with the same shortest decimal form, so 0.2 stays 0.2.
func shortestFloat(value float32) float64 {
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return float64(value)
	}
	parsed, err := strconv.ParseFloat(strconv.FormatFloat(float64(value), 'g', -1, 32), 64)
	if err != nil {
		return float64(value)
	}
	return parsed
}

func snakeCase(name string) string {
	var builder strings.Builder
	for index, character := range name {
		if unicode.IsUpper(character) {
			if index > 0 {
				builder.WriteByte('_')
			}
			character = unicode.ToLower(character)
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func uint32Field(message protoreflect.Message, name protoreflect.Name) uint32 {
	if field := message.Descriptor().Fields().ByName(name); field != nil {
		return uint32(message.Get(field).Uint()) //nolint:gosec // Fixed32 and uint32 fields fit.
	}
	return 0
}

func stringField(message protoreflect.Message, name protoreflect.Name) string {
	if field := message.Descriptor().Fields().ByName(name); field != nil && field.Kind() == protoreflect.StringKind {
		return message.Get(field).String()
	}
	return ""
}

func boolField(message protoreflect.Message, name protoreflect.Name) bool {
	if field := message.Descriptor().Fields().ByName(name); field != nil && field.Kind() == protoreflect.BoolKind {
		return message.Get(field).Bool()
	}
	return false
}
