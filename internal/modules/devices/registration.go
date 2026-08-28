package devices

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

const (
	minimumDescriptorLength = 1
	maximumNameLength       = 128
	maximumExternalIDLength = 256
	maximumTypeIDLength     = 128
)

var registrationSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type Registration struct {
	BindingKey string
	Device     DeviceDescriptor
	Entities   []EntityDescriptor
}

type DeviceDescriptor struct {
	ExternalID *string
	Name       string
	Kind       DeviceKind
}

type EntityDescriptor struct {
	Key              string
	ExternalID       string
	Name             string
	TypeID           EntityTypeID
	Support          EntitySupport
	InitiallyEnabled *bool
}

type Binding struct {
	BindingKey string
	DeviceID   DeviceID
	Entities   []EntityBinding
}

type EntityBinding struct {
	Key      string
	EntityID EntityID
	Enabled  bool
}

type RegistrationRejectionCode string

const (
	RegistrationInvalidDescriptor   RegistrationRejectionCode = "invalid_descriptor"
	RegistrationImmutableTypeChange RegistrationRejectionCode = "immutable_type_change"
	RegistrationIdentityConflict    RegistrationRejectionCode = "identity_conflict"
)

type RegistrationRejectedError struct {
	Code    RegistrationRejectionCode
	Message string
}

func (err *RegistrationRejectedError) Error() string {
	return fmt.Sprintf("registration rejected (%s): %s", err.Code, err.Message)
}

func (service *Service) Register(ctx context.Context, adapterID string, registration Registration) (Binding, error) {
	normalized, err := service.normalizeRegistration(adapterID, registration)
	if err != nil {
		return Binding{}, &RegistrationRejectedError{
			Code:    RegistrationInvalidDescriptor,
			Message: operatorMessage(err.Error()),
		}
	}
	deviceID, err := service.dependencies.NewDeviceID()
	if err != nil {
		return Binding{}, fmt.Errorf("generate device ID: %w", err)
	}
	entities := make([]RegisterEntityParams, len(normalized.Entities))
	for index, entity := range normalized.Entities {
		entityID, entityIDErr := service.dependencies.NewEntityID()
		if entityIDErr != nil {
			return Binding{}, fmt.Errorf("generate entity ID: %w", entityIDErr)
		}
		entities[index] = RegisterEntityParams{EntityID: entityID, Entity: copyEntityDescriptor(entity)}
	}
	registeredAt := service.dependencies.Now().UTC()
	params := RegisterBindingParams{
		AdapterID:  adapterID,
		BindingKey: normalized.BindingKey,
		DeviceID:   deviceID,
		Device:     copyDeviceDescriptor(normalized.Device),
		Entities:   entities,
		UpdatedAt:  registeredAt,
	}
	binding, err := service.repository.RegisterBinding(ctx, params)
	if errors.Is(err, errImmutableTypeChange) {
		return Binding{}, &RegistrationRejectedError{
			Code: RegistrationImmutableTypeChange, Message: "an existing entity cannot change type",
		}
	}
	if errors.Is(err, errIdentityConflict) {
		return Binding{}, &RegistrationRejectedError{
			Code: RegistrationIdentityConflict, Message: "the binding or external ID is already assigned",
		}
	}
	if err != nil {
		return Binding{}, err
	}
	return binding, nil
}

//nolint:gocognit // Validation follows the nested registration document in one linear pass.
func (service *Service) normalizeRegistration(adapterID string, registration Registration) (Registration, error) {
	normalized := copyRegistration(registration)
	if !registrationSlugPattern.MatchString(adapterID) {
		return Registration{}, errors.New("adapter ID must be a subject-safe slug")
	}
	if !registrationSlugPattern.MatchString(normalized.BindingKey) {
		return Registration{}, errors.New("binding key must be a subject-safe slug")
	}
	if normalized.Device.Kind != DeviceKindLight {
		return Registration{}, errors.New("device kind must be light")
	}
	if !validLength(normalized.Device.Name, maximumNameLength) {
		return Registration{}, errors.New("device name must contain 1 to 128 characters")
	}
	if normalized.Device.ExternalID != nil && !validLength(*normalized.Device.ExternalID, maximumExternalIDLength) {
		return Registration{}, errors.New("device external ID must contain 1 to 256 characters")
	}
	if len(normalized.Entities) < 1 || len(normalized.Entities) > 64 {
		return Registration{}, errors.New("registration must contain 1 to 64 entities")
	}
	keys := make(map[string]struct{}, len(normalized.Entities))
	externalIDs := make(map[string]struct{}, len(normalized.Entities))
	for index, entity := range normalized.Entities {
		if !registrationSlugPattern.MatchString(entity.Key) {
			return Registration{}, errors.New("entity key must be a subject-safe slug")
		}
		if _, exists := keys[entity.Key]; exists {
			return Registration{}, errors.New("entity keys must be unique within a registration")
		}
		keys[entity.Key] = struct{}{}
		if !validLength(entity.ExternalID, maximumExternalIDLength) {
			return Registration{}, errors.New("entity external ID must contain 1 to 256 characters")
		}
		if _, exists := externalIDs[entity.ExternalID]; exists {
			return Registration{}, errors.New("entity external IDs must be unique within a registration")
		}
		externalIDs[entity.ExternalID] = struct{}{}
		if !validLength(entity.Name, maximumNameLength) {
			return Registration{}, errors.New("entity name must contain 1 to 128 characters")
		}
		if !validLength(string(entity.TypeID), maximumTypeIDLength) {
			return Registration{}, errors.New("entity type must contain 1 to 128 characters")
		}
		support, err := service.catalog.NormalizeSupport(entity.TypeID, entity.Support)
		if err != nil {
			return Registration{}, fmt.Errorf("entity descriptor is incompatible with its type: %w", err)
		}
		normalized.Entities[index].Support = support
	}
	return normalized, nil
}

func operatorMessage(message string) string {
	const maximum = 512
	runes := []rune(message)
	if len(runes) <= maximum {
		return message
	}
	return string(runes[:maximum-1]) + "…"
}

func validLength(value string, maximum int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	return length >= minimumDescriptorLength && length <= maximum
}

func copyRegistration(registration Registration) Registration {
	cloned := registration
	cloned.Device = copyDeviceDescriptor(registration.Device)
	cloned.Entities = make([]EntityDescriptor, len(registration.Entities))
	for index, entity := range registration.Entities {
		cloned.Entities[index] = copyEntityDescriptor(entity)
	}
	return cloned
}

func copyDeviceDescriptor(device DeviceDescriptor) DeviceDescriptor {
	cloned := device
	if device.ExternalID != nil {
		externalID := *device.ExternalID
		cloned.ExternalID = &externalID
	}
	return cloned
}

func copyEntityDescriptor(entity EntityDescriptor) EntityDescriptor {
	cloned := entity
	cloned.Support = append(EntitySupport(nil), entity.Support...)
	if entity.InitiallyEnabled != nil {
		initiallyEnabled := *entity.InitiallyEnabled
		cloned.InitiallyEnabled = &initiallyEnabled
	}
	return cloned
}
