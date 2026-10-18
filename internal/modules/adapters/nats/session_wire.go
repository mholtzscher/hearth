package nats

const (
	statusAccepted        = "accepted"
	statusRejected        = "rejected"
	runtimeFencedCode     = "runtime_fenced"
	runtimeFencedMessage  = "Adapter runtime is fenced"
	transportEventKey     = "event"
	transportErrorCodeKey = "error_code"
)

type adapterClaimRequest struct {
	AdapterID       string `json:"adapter_id"`
	RuntimeID       string `json:"runtime_id"`
	SoftwareName    string `json:"software_name"`
	SoftwareVersion string `json:"software_version"`
}

type adapterClaimResponse struct {
	Status string             `json:"status"`
	Error  *adapterClaimError `json:"error,omitempty"`
}

type adapterClaimError struct {
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	RetryAfter *string `json:"retry_after,omitempty"`
}

type adapterHeartbeatRequest struct {
	ExternalSystem externalSystemHealth `json:"external_system"`
}

type externalSystemHealth struct {
	Status           string        `json:"status"`
	SourceObservedAt string        `json:"source_observed_at"`
	Reason           *healthReason `json:"reason,omitempty"`
}

type healthReason struct {
	Code string `json:"code"`
}

type adapterHeartbeatResponse struct {
	Status         string        `json:"status"`
	LeaseExpiresAt string        `json:"lease_expires_at,omitempty"`
	Error          *adapterError `json:"error,omitempty"`
}

type adapterReleaseRequest struct{}

type adapterReleaseResponse struct {
	Status string        `json:"status"`
	Error  *adapterError `json:"error,omitempty"`
}

type adapterError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
