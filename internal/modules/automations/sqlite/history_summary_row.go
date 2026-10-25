package sqlite

import "github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"

// historySummaryRow contains only the persisted summary contract. Both listing
// projections must have this shape; neither can supply detail-only documents.
type historySummaryRow dbsqlc.ListHistoryFirstPageRow

// historySummaryColumns lets full detail reads share the provenance validators
// without making collection reads construct an incomplete full history row.
func historySummaryColumns(row dbsqlc.AutomationHistory) historySummaryRow {
	return historySummaryRow{
		ID: row.ID, AutomationID: row.AutomationID, AutomationName: row.AutomationName,
		Kind: row.Kind, Revision: row.Revision, RecordedAt: row.RecordedAt,
		RunStatus: row.RunStatus, RunSource: row.RunSource,
		SkipReason: row.SkipReason, SkipSource: row.SkipSource,
		FactID: row.FactID, FactFamily: row.FactFamily, FactEntityID: row.FactEntityID,
		FactVariant: row.FactVariant, FactCausationID: row.FactCausationID,
		FactValueJson: row.FactValueJson, FactPreviousValueJson: row.FactPreviousValueJson,
		FactEmittedAt: row.FactEmittedAt,
		HoldTriggerID: row.HoldTriggerID, HoldStartedAt: row.HoldStartedAt, HoldDueAt: row.HoldDueAt,
		ConditionMode: row.ConditionMode, ConditionResult: row.ConditionResult,
		ConditionBypassed: row.ConditionBypassed,
	}
}
