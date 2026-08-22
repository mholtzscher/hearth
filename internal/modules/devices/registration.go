package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
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
	Key                 string
	ExternalID          string
	Name                string
	TypeID              EntityTypeID
	Constraints         json.RawMessage
	SupportedOperations []OperationName
}

type Binding struct {
	BindingKey string
	DeviceID   DeviceID
	Entities   []EntityBinding
}

type EntityBinding struct {
	Key      string
	EntityID EntityID
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
	if err := service.validateRegistration(adapterID, registration); err != nil {
		return Binding{}, &RegistrationRejectedError{Code: RegistrationInvalidDescriptor, Message: operatorMessage(err.Error())}
	}
	deviceID, err := service.dependencies.NewDeviceID()
	if err != nil {
		return Binding{}, fmt.Errorf("generate device ID: %w", err)
	}
	entityID, err := service.dependencies.NewEntityID()
	if err != nil {
		return Binding{}, fmt.Errorf("generate entity ID: %w", err)
	}
	registeredAt := service.dependencies.Now().UTC()
	entity := registration.Entities[0]
	params := RegisterBindingParams{
		AdapterID:  adapterID,
		BindingKey: registration.BindingKey,
		DeviceID:   deviceID,
		EntityID:   entityID,
		Device:     copyDeviceDescriptor(registration.Device),
		Entity:     copyEntityDescriptor(entity),
		UpdatedAt:  registeredAt,
	}
	binding, err := service.registration.RegisterBinding(ctx, params)
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

func (service *Service) validateRegistration(adapterID string, registration Registration) error {
	if !registrationSlugPattern.MatchString(adapterID) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	if !registrationSlugPattern.MatchString(registration.BindingKey) {
		return errors.New("binding key must be a subject-safe slug")
	}
	if registration.Device.Kind != DeviceKindLight {
		return errors.New("device kind must be light")
	}
	if !validLength(registration.Device.Name, 1, 128) {
		return errors.New("device name must contain 1 to 128 characters")
	}
	if registration.Device.ExternalID != nil && !validLength(*registration.Device.ExternalID, 1, 256) {
		return errors.New("device external ID must contain 1 to 256 characters")
	}
	if len(registration.Entities) != 1 {
		return errors.New("registration must contain exactly one entity")
	}
	entity := registration.Entities[0]
	if !registrationSlugPattern.MatchString(entity.Key) {
		return errors.New("entity key must be a subject-safe slug")
	}
	if !validLength(entity.ExternalID, 1, 256) {
		return errors.New("entity external ID must contain 1 to 256 characters")
	}
	if !validLength(entity.Name, 1, 128) {
		return errors.New("entity name must contain 1 to 128 characters")
	}
	if !validLength(string(entity.TypeID), 1, 128) {
		return errors.New("entity type must contain 1 to 128 characters")
	}
	if err := service.catalog.ValidateEntity(entity.TypeID, entity.Constraints, entity.SupportedOperations); err != nil {
		return fmt.Errorf("entity descriptor is incompatible with its type: %w", err)
	}
	return nil
}

func operatorMessage(message string) string {
	const maximum = 512
	runes := []rune(message)
	if len(runes) <= maximum {
		return message
	}
	return string(runes[:maximum-1]) + "…"
}

func validLength(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func copyDeviceDescriptor(device DeviceDescriptor) DeviceDescriptor {
	copy := device
	if device.ExternalID != nil {
		externalID := *device.ExternalID
		copy.ExternalID = &externalID
	}
	return copy
}

func copyEntityDescriptor(entity EntityDescriptor) EntityDescriptor {
	copy := entity
	copy.Constraints = append(json.RawMessage(nil), entity.Constraints...)
	copy.SupportedOperations = append([]OperationName(nil), entity.SupportedOperations...)
	return copy
}
