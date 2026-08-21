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

type ValidationError struct {
	Err error
}

func (err *ValidationError) Error() string {
	return "adapter validation: " + err.Err.Error()
}

func (err *ValidationError) Unwrap() error {
	return err.Err
}
