package v1

import "embed"

const (
	CommonSchemaID                     = "urn:hearth:schema:common:v1"
	AdapterClaimRequestSchemaID        = "urn:hearth:schema:adapter-claim-request:v1"
	AdapterClaimResponseSchemaID       = "urn:hearth:schema:adapter-claim-response:v1"
	AdapterHeartbeatRequestSchemaID    = "urn:hearth:schema:adapter-heartbeat-request:v1"
	AdapterHeartbeatResponseSchemaID   = "urn:hearth:schema:adapter-heartbeat-response:v1"
	AdapterReleaseRequestSchemaID      = "urn:hearth:schema:adapter-release-request:v1"
	AdapterReleaseResponseSchemaID     = "urn:hearth:schema:adapter-release-response:v1"
	EntityAvailabilityRequestSchemaID  = "urn:hearth:schema:entity-availability-request:v1"
	EntityAvailabilityResponseSchemaID = "urn:hearth:schema:entity-availability-response:v1"
	RegistrationRequestSchemaID        = "urn:hearth:schema:registration-request:v1"
	RegistrationResponseSchemaID       = "urn:hearth:schema:registration-response:v1"
	ObservationSchemaID                = "urn:hearth:schema:observation:v1"
	CommandRequestSchemaID             = "urn:hearth:schema:command-request:v1"
	CommandResponseSchemaID            = "urn:hearth:schema:command-response:v1"
	EntityEnablementRequestSchemaID    = "urn:hearth:schema:entity-enablement-request:v1"
	EntityEnablementResponseSchemaID   = "urn:hearth:schema:entity-enablement-response:v1"
	OwnedMappingsRequestSchemaID       = "urn:hearth:schema:owned-mappings-request:v1"
	OwnedMappingsResponseSchemaID      = "urn:hearth:schema:owned-mappings-response:v1"
)

// FS contains the authoritative Hearth v1 JSON Schemas.
//
//go:embed *.schema.json
var FS embed.FS

func SchemaFiles() map[string]string {
	return map[string]string{
		CommonSchemaID:                     "common.schema.json",
		AdapterClaimRequestSchemaID:        "adapter-claim-request.schema.json",
		AdapterClaimResponseSchemaID:       "adapter-claim-response.schema.json",
		AdapterHeartbeatRequestSchemaID:    "adapter-heartbeat-request.schema.json",
		AdapterHeartbeatResponseSchemaID:   "adapter-heartbeat-response.schema.json",
		AdapterReleaseRequestSchemaID:      "adapter-release-request.schema.json",
		AdapterReleaseResponseSchemaID:     "adapter-release-response.schema.json",
		EntityAvailabilityRequestSchemaID:  "entity-availability-request.schema.json",
		EntityAvailabilityResponseSchemaID: "entity-availability-response.schema.json",
		RegistrationRequestSchemaID:        "registration-request.schema.json",
		RegistrationResponseSchemaID:       "registration-response.schema.json",
		ObservationSchemaID:                "observation.schema.json",
		CommandRequestSchemaID:             "command-request.schema.json",
		CommandResponseSchemaID:            "command-response.schema.json",
		EntityEnablementRequestSchemaID:    "entity-enablement-request.schema.json",
		EntityEnablementResponseSchemaID:   "entity-enablement-response.schema.json",
		OwnedMappingsRequestSchemaID:       "owned-mappings-request.schema.json",
		OwnedMappingsResponseSchemaID:      "owned-mappings-response.schema.json",
	}
}
