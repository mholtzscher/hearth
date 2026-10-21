package mcpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type AutomationReceipt struct {
	AutomationID string `json:"automation_id"`
	Revision     int64  `json:"revision"`
}

func automationReceipt(record automations.Record) AutomationReceipt {
	return AutomationReceipt{AutomationID: string(record.ID), Revision: record.Revision}
}
