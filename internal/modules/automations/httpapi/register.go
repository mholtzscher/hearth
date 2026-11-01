package httpapi

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Register publishes Automation operations. A nil logger discards HTTP diagnostics.
func Register(api huma.API, service Automations, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	publishDefinitionSchema(api, automations.AutomationDefinitionSchema())
	publishReplacementSchema(api)
	publishOutputSchemas(api)
	handler := &handler{automations: service, logger: logger}
	registerCreateAutomation(api, handler)
	registerListAutomations(api, handler)
	registerGetAutomation(api, handler)
	registerReplaceAutomation(api, handler)
	registerDeleteAutomation(api, handler)
	registerStartAutomationRun(api, handler)
	registerListHistory(api, handler)
	registerGetHistoryEntry(api, handler)
}
