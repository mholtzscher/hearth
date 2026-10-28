package automations_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func literalJSON(raw string) automations.ValueBinding {
	return automations.ValueBinding{Body: automations.LiteralValue{Value: json.RawMessage(raw)}}
}
func literalText(value string) automations.ValueBinding { return literalJSON(strconv.Quote(value)) }
func literalBytes(binding automations.ValueBinding) json.RawMessage {
	return binding.Body.(automations.LiteralValue).Value
}

// newEntityID mints one canonical Entity identity for a fixture.
func newEntityID(t *testing.T) devices.EntityID {
	t.Helper()
	id, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
