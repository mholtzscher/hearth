package api

import "github.com/danielgtaylor/huma/v2"

type EntityBody struct {
	ID       string         `json:"id"`
	DeviceID string         `json:"device_id"`
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Support  map[string]any `json:"support"`
	State    *StateBody     `json:"state"`
}

type StateBody struct {
	// Huma uses this ignored marker to emit StateBody as an object-or-null schema.
	_                 struct{} `json:"-" nullable:"true"`
	Value             any      `json:"value"`
	ObservationID     string   `json:"observation_id"`
	AdapterReceivedAt string   `json:"adapter_received_at"`
	SourceUpdatedAt   *string  `json:"source_updated_at,omitempty"`
	ObservedAt        string   `json:"observed_at"`
}

type CommandBody struct {
	OperationName string         `json:"operation"`
	Parameters    map[string]any `json:"parameters"`
}

type CommandResultBody struct {
	CommandID     string `json:"command_id"`
	Status        string `json:"status"`
	ObservationID string `json:"observation_id"`
	Value         any    `json:"value"`
}

type ErrorBody struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	CommandID *string `json:"command_id,omitempty"`
}

type statusError struct {
	ErrorBody
	status int
}

func NewStatusError(status int, code, message string) huma.StatusError {
	return &statusError{
		status:    status,
		ErrorBody: ErrorBody{Error: APIError{Code: code, Message: message}},
	}
}

func (err *statusError) Error() string {
	return err.ErrorBody.Error.Message
}

func (err *statusError) GetStatus() int {
	return err.status
}
