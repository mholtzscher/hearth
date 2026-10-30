package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// decodeDefinitionBody strictly decodes one definition payload.
func decodeDefinitionBody(raw json.RawMessage) (automations.Definition, error) {
	return decodeDefinitionBodyAt(raw, "")
}

func decodeDefinitionBodyAt(raw json.RawMessage, prefix string) (automations.Definition, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return automations.Definition{}, newProblem(
			http.StatusBadRequest, "invalid_definition", "definition body is required",
		)
	}
	definition, err := automations.DecodeDefinition(raw)
	if err != nil {
		return automations.Definition{}, mapDomainErrorAt(err, prefix)
	}
	return definition, nil
}

// definitionCodec is the canonical strict definition schema, compiled once.
//
//nolint:gochecknoglobals // One immutable compiled schema, never reassigned.
var definitionCodec = sync.OnceValues(automations.NewDefinitionCodec)
