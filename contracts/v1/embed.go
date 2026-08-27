package v1

import "embed"

const (
	CommonSchemaID                   = "urn:hearth:schema:common:v1"
	RegistrationRequestSchemaID      = "urn:hearth:schema:registration-request:v1"
	RegistrationResponseSchemaID     = "urn:hearth:schema:registration-response:v1"
	ObservationSchemaID              = "urn:hearth:schema:observation:v1"
	CommandRequestSchemaID           = "urn:hearth:schema:command-request:v1"
	CommandResponseSchemaID          = "urn:hearth:schema:command-response:v1"
	EntityEnablementRequestSchemaID  = "urn:hearth:schema:entity-enablement-request:v1"
	EntityEnablementResponseSchemaID = "urn:hearth:schema:entity-enablement-response:v1"
)

// FS contains the authoritative Hearth v1 JSON Schemas.
//
//go:embed *.schema.json
var FS embed.FS

func SchemaFiles() map[string]string {
	return map[string]string{
		CommonSchemaID:                   "common.schema.json",
		RegistrationRequestSchemaID:      "registration-request.schema.json",
		RegistrationResponseSchemaID:     "registration-response.schema.json",
		ObservationSchemaID:              "observation.schema.json",
		CommandRequestSchemaID:           "command-request.schema.json",
		CommandResponseSchemaID:          "command-response.schema.json",
		EntityEnablementRequestSchemaID:  "entity-enablement-request.schema.json",
		EntityEnablementResponseSchemaID: "entity-enablement-response.schema.json",
	}
}
