package powerv1

import (
	"embed"
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/entitytypes"
)

const (
	StateSchemaID         = "urn:hearth:schema:entity-type:power:v1:state"
	SupportSchemaID       = "urn:hearth:schema:entity-type:power:v1:support"
	SetParametersSchemaID = "urn:hearth:schema:entity-type:power:v1:set-parameters"
)

// FS contains the authoritative power/v1 JSON Schemas.
//
//go:embed *.schema.json
var FS embed.FS

func SchemaFiles() map[string]string {
	return map[string]string{
		StateSchemaID:         "state.schema.json",
		SupportSchemaID:       "support.schema.json",
		SetParametersSchemaID: "set-parameters.schema.json",
	}
}

func Compile() (*Codecs, error) {
	state, err := compileCodec[State](StateSchemaID, "state.schema.json")
	if err != nil {
		return nil, err
	}
	support, err := compileCodec[Support](SupportSchemaID, "support.schema.json")
	if err != nil {
		return nil, err
	}
	parameters, err := compileCodec[SetParameters](SetParametersSchemaID, "set-parameters.schema.json")
	if err != nil {
		return nil, err
	}
	return &Codecs{State: state, Support: support, SetParameters: parameters}, nil
}

func compileCodec[T any](schemaID, path string) (*entitytypes.JSONCodec[T], error) {
	raw, err := FS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return entitytypes.CompileJSONCodec[T](schemaID, json.RawMessage(raw), nil)
}
