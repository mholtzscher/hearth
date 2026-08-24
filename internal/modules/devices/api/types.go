package api

type EntityBody struct {
	ID       string         `json:"id"`
	DeviceID string         `json:"device_id"`
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Support  map[string]any `json:"support"`
	State    *StateBody     `json:"state"`
}

type StateBody struct {
	Value             any     `json:"value"`
	ObservationID     string  `json:"observation_id"`
	AdapterReceivedAt string  `json:"adapter_received_at"`
	SourceUpdatedAt   *string `json:"source_updated_at,omitempty"`
	ObservedAt        string  `json:"observed_at"`
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

func (err *statusError) Error() string {
	return err.ErrorBody.Error.Message
}

func (err *statusError) GetStatus() int {
	return err.status
}
