package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerStartAutomationRun(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[startAutomationRunToolInput, RunReceipt]{
		Output:      mustContract(mcptools.PrepareOutput[RunReceipt](runReceiptSchema())),
		Name:        "start_automation_run",
		Description: "Admit a manual Automation Run and return its current status receipt; admission is not completion. Inspect evidence with get_automation_history_entry using entry_id = run_id",
		Handler:     handler.startAutomationRun,
	})
}

type startAutomationRunToolInput struct {
	AutomationID     automationID `json:"automation_id"               jsonschema:"Canonical Hearth Automation ID"`
	BypassConditions bool         `json:"bypass_conditions,omitempty" jsonschema:"Admit without evaluating configured Conditions"`
}

func (handler *handler) startAutomationRun(
	ctx context.Context,
	input startAutomationRunToolInput,
) (RunReceipt, error) {
	run, err := handler.automations.StartManualRun(ctx, automations.ManualRunInput{
		AutomationID:     automations.AutomationID(input.AutomationID),
		BypassConditions: input.BypassConditions,
	})
	if err != nil {
		return RunReceipt{}, automationFailure(err, failureAutomationNotFound)
	}
	return runReceipt(run), nil
}

//nolint:goconst // Wire field names stay literal beside this operation's result contract.
func runReceiptSchema() map[string]any {
	runReceipt := objectSchema(
		map[string]any{
			automationIDField: stringSchema(),
			"run_id":          stringSchema(),
			"status":          enumSchema("running", "succeeded", "failed", "interrupted"),
			"completed_at":    timeSchema(),
			"failure_code":    stringSchema(),
		},
		automationIDField,
		"run_id",
		"status",
	)
	runReceipt["oneOf"] = runLifecycleSchema()
	return runReceipt
}
