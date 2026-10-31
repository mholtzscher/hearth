package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerReplaceAutomation(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[replaceAutomationToolInput, AutomationReceipt]{
		Name:        "replace_automation",
		Description: "Replace an Automation and return its committed identity and revision receipt using definition schema v1. Every Step requires kind, including command. Observation comparisons address the Observation value directly: use value_pointer \"\" for scalar values and never /state/value; the pointer alias is invalid.",
		Handler:     handler.replaceAutomation,
	})
}

type replaceAutomationToolInput struct {
	AutomationID     automationID     `json:"automation_id"     doc:"Canonical Hearth Automation ID"`
	ExpectedRevision expectedRevision `json:"expected_revision" doc:"Current revision the replacement expects"`
	Definition       definitionValue  `json:"definition"        doc:"The replacement strict Automation definition document"`
}

func (replaceAutomationToolInput) ValidateEnvelope(raw json.RawMessage) error {
	return validateDefinitionEnvelope(raw)
}

func (handler *handler) replaceAutomation(
	ctx context.Context,
	input replaceAutomationToolInput,
) (AutomationReceipt, error) {
	record, err := handler.automations.ReplaceAutomation(
		ctx,
		automations.AutomationID(input.AutomationID),
		int64(input.ExpectedRevision),
		input.Definition.value,
	)
	if err != nil {
		return AutomationReceipt{}, automationFailure(err, failureAutomationNotFound)
	}
	return automationReceipt(record), nil
}
