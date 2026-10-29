package httpapi

import (
	"fmt"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
)

// Register publishes Automation operations. A nil logger discards HTTP diagnostics.
func Register(api huma.API, service Automations, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	codec, err := definitionCodec()
	if err != nil {
		panic(fmt.Errorf("automation API definition schema: %w", err))
	}
	publishDefinitionSchema(api, codec.AutomationDefinitionSchema())
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
