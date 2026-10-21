package mcpapi

import mcptools "github.com/mholtzscher/hearth/internal/mcpapi"

func Register(server *mcptools.Server, service Automations) {
	handler := &handler{automations: service}
	registerCreateAutomation(server, handler)
	registerListAutomations(server, handler)
	registerGetAutomation(server, handler)
	registerReplaceAutomation(server, handler)
	registerDeleteAutomation(server, handler)
	registerStartAutomationRun(server, handler)
	registerListAutomationHistory(server, handler)
	registerGetAutomationHistoryEntry(server, handler)
}
