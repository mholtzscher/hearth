package httpapi

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AutomationDefinitionBody reuses the canonical concrete Step, Trigger, and
// Condition DTO mapping. Encoding validates recursive bounds before copying.
// HTTP publish the canonical schema rather than reflecting these bytes.
type AutomationDefinitionBody struct{ encoded json.RawMessage }

func (body AutomationDefinitionBody) MarshalJSON() ([]byte, error) {
	return body.encoded.MarshalJSON()
}

// AutomationBody is one current definition with its revision and timestamps.
type AutomationBody struct {
	ID         string                   `json:"id"`
	Revision   int64                    `json:"revision"`
	CreatedAt  time.Time                `json:"created_at"`
	UpdatedAt  time.Time                `json:"updated_at"`
	Definition AutomationDefinitionBody `json:"definition"`
}

// AutomationOutput carries a creation Location or a normal definition read.
type AutomationOutput struct {
	Location string `header:"Location"`
	Body     AutomationBody
}

func automationDefinitionBody(definition automations.Definition) AutomationDefinitionBody {
	raw, err := automations.EncodeDefinition(definition)
	if err != nil {
		panic(fmt.Errorf("invalid retained definition: %w", err))
	}
	return AutomationDefinitionBody{encoded: raw}
}

func automationBody(record automations.Record) AutomationBody {
	return AutomationBody{
		ID:         string(record.ID),
		Revision:   record.Revision,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  record.UpdatedAt,
		Definition: automationDefinitionBody(record.Definition),
	}
}
