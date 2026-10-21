package mcpapi

import "encoding/json"

type AvailabilitySummary struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code,omitempty"`
}

type StateSummary struct {
	Value      json.RawMessage `json:"value"`
	ObservedAt string          `json:"observed_at"`
}

type EntitySummary struct {
	EntityID     string              `json:"entity_id"`
	DeviceID     string              `json:"device_id"`
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Enabled      bool                `json:"enabled"`
	Availability AvailabilitySummary `json:"availability"`
	State        *StateSummary       `json:"state"`
}
