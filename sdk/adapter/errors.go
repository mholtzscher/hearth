package adapter

import (
	"errors"
	"fmt"
)

var (
	ErrAlreadyResponded = errors.New("adapter: command already responded")
	ErrMissingResponse  = errors.New("adapter: command handler returned without a response")
	ErrClosed           = errors.New("adapter: session closed")
)

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

type EntityEnablementRejectionCode string

const (
	EntityEnablementUnknownEntity EntityEnablementRejectionCode = "unknown_entity"
	EntityEnablementWrongAdapter  EntityEnablementRejectionCode = "wrong_adapter"
)

type EntityEnablementRejectedError struct {
	Code    EntityEnablementRejectionCode
	Message string
}

func (err *EntityEnablementRejectedError) Error() string {
	return fmt.Sprintf("entity enablement rejected (%s): %s", err.Code, err.Message)
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
