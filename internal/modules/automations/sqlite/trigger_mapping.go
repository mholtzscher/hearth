package sqlite

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// encodeTriggerIDs renders the matched Trigger ID list of one Run as JSON. A
// manual Run carries no matched IDs, which encodes as an empty array.
func encodeTriggerIDs(ids []automations.TriggerID) (json.RawMessage, error) {
	if ids == nil {
		ids = []automations.TriggerID{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: matched trigger IDs cannot be encoded: %w", automations.ErrInvalidAutomation, err,
		)
	}
	return raw, nil
}

// decodeTriggerIDs decodes a stored matched Trigger ID list, rejecting any
// identity that is not a canonical Trigger ID.
func decodeTriggerIDs(raw json.RawMessage) ([]automations.TriggerID, error) {
	var ids []automations.TriggerID
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := automations.ParseTriggerID(string(id)); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
