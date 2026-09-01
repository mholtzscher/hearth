package adapter

import (
	"errors"
	"fmt"
)

var (
	ErrAlreadyResponded = errors.New("adapter: command already responded")
	ErrMissingResponse  = errors.New("adapter: command handler returned without a response")
	ErrClosed           = errors.New("adapter: session closed")
	ErrRuntimeFenced    = errors.New("adapter: runtime fenced")
)

type RegistrationRejectionCode string

const (
	RegistrationInvalidDescriptor   RegistrationRejectionCode = "invalid_descriptor"
	RegistrationImmutableTypeChange RegistrationRejectionCode = "immutable_type_change"
	RegistrationIdentityConflict    RegistrationRejectionCode = "identity_conflict"
	registrationRuntimeFenced       RegistrationRejectionCode = "runtime_fenced"
)

type RegistrationRejectedError struct {
	Code    RegistrationRejectionCode
	Message string
}

func (err *RegistrationRejectedError) Error() string {
	return fmt.Sprintf("registration rejected (%s): %s", err.Code, err.Message)
}

type EntityEnablementRejectionCode string

const (
	EntityEnablementUnknownEntity EntityEnablementRejectionCode = "unknown_entity"
	EntityEnablementWrongAdapter  EntityEnablementRejectionCode = "wrong_adapter"
	entityEnablementRuntimeFenced EntityEnablementRejectionCode = "runtime_fenced"
)

type EntityEnablementRejectedError struct {
	Code    EntityEnablementRejectionCode
	Message string
}

func (err *EntityEnablementRejectedError) Error() string {
	return fmt.Sprintf("entity enablement rejected (%s): %s", err.Code, err.Message)
}

type EntityAvailabilityRejectionCode string

const (
	EntityAvailabilityAdapterUnhealthy EntityAvailabilityRejectionCode = "adapter_unhealthy"
	EntityAvailabilityUnknownEntity    EntityAvailabilityRejectionCode = "unknown_entity"
	EntityAvailabilityWrongAdapter     EntityAvailabilityRejectionCode = "wrong_adapter"
	entityAvailabilityRuntimeFenced    EntityAvailabilityRejectionCode = "runtime_fenced"
)

type EntityAvailabilityRejectedError struct {
	Code     EntityAvailabilityRejectionCode
	Message  string
	EntityID string
}

func (err *EntityAvailabilityRejectedError) Error() string {
	if err.EntityID == "" {
		return fmt.Sprintf("entity availability rejected (%s): %s", err.Code, err.Message)
	}
	return fmt.Sprintf("entity availability rejected for %s (%s): %s", err.EntityID, err.Code, err.Message)
}

type ValidationError struct {
	Err error
}

func (err *ValidationError) Error() string {
	return "adapter validation: " + err.Err.Error()
}

func (err *ValidationError) Unwrap() error {
	return err.Err
}
