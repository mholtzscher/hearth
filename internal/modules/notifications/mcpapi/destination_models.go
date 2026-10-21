package mcpapi

type Destination struct {
	DestinationID string `json:"destination_id"`
	Name          string `json:"name"`
	Available     bool   `json:"available"`
}
