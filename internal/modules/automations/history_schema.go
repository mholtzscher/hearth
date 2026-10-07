package automations

import (
	_ "embed"
	"encoding/json"
)

//go:embed automation-history.schema.json
var automationHistorySchema []byte

// AutomationHistorySchema returns an owned copy of the public history contract.
func AutomationHistorySchema() json.RawMessage {
	return append(json.RawMessage(nil), automationHistorySchema...)
}
