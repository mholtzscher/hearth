package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

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
