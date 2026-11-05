package dbload

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	agentsql "github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	dashboardssqlite "github.com/mholtzscher/hearth/internal/modules/dashboards/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationssqlite "github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
)

func checkRepositoryDecoding(ctx context.Context, database *sql.DB, manifest Manifest) error {
	if err := checkDeviceDecoding(ctx, database, manifest); err != nil {
		return err
	}
	if err := checkAutomationDecoding(ctx, database, manifest); err != nil {
		return err
	}
	if err := checkNotificationDecoding(ctx, database, manifest); err != nil {
		return err
	}
	if err := checkFunctionDecoding(ctx, database, manifest); err != nil {
		return err
	}
	if err := checkDashboardDecoding(ctx, database, manifest); err != nil {
		return err
	}
	return checkAgentDecoding(ctx, database, manifest)
}

func checkDeviceDecoding(ctx context.Context, database *sql.DB, manifest Manifest) error {
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		return fmt.Errorf("build Entity type catalog: %w", err)
	}
	repository := devicessqlite.NewDeviceRepository(database, catalog)
	entityID := devices.EntityID(manifest.EntityID)
	entity, err := repository.GetEntity(ctx, entityID)
	if err != nil {
		return fmt.Errorf("%w: decode Entity: %w", errFixtureDomain, err)
	}
	if entity.Entity.ID != entityID || entity.Entity.DeviceID != devices.DeviceID(manifest.DeviceID) ||
		entity.State == nil || entity.State.ObservationID == "" || len(entity.State.Value) == 0 ||
		len(entity.Entity.Support) == 0 {
		return fmt.Errorf("%w: representative Entity decoded incompletely", errFixtureDomain)
	}
	snapshot, err := repository.GetEntityStateSnapshot(
		ctx, []devices.EntityID{entityID, devices.EntityID(manifest.EventEntityID)},
	)
	if err != nil || len(snapshot.Entries) != 2 {
		return fmt.Errorf("%w: decode Entity state snapshot: %w", errFixtureDomain, err)
	}
	if err = checkCommandDecoding(ctx, database, repository, manifest, entityID); err != nil {
		return err
	}
	return checkEntityHistoryDecoding(ctx, database, repository, manifest, entityID)
}

func checkCommandDecoding(
	ctx context.Context,
	database *sql.DB,
	repository *devicessqlite.DeviceRepository,
	manifest Manifest,
	entityID devices.EntityID,
) error {
	commandCount, err := fixtureCount(
		ctx,
		database,
		"SELECT COUNT(*) FROM commands WHERE entity_id = ?",
		manifest.EntityID,
	)
	if err != nil {
		return err
	}
	page, err := repository.ListEntityCommands(ctx, devices.ListEntityCommandsParams{
		EntityID: entityID, Limit: validationPageLimit,
	})
	if err != nil || int64(len(page.Items)) != minInt64(commandCount) {
		return fmt.Errorf("%w: decode Entity Commands: %w", errFixtureDomain, err)
	}
	for _, command := range page.Items {
		if command.EntityID != entityID || command.ID == "" {
			return fmt.Errorf("%w: decoded Command has the wrong owner", errFixtureDomain)
		}
	}
	return nil
}

func checkEntityHistoryDecoding(
	ctx context.Context,
	database *sql.DB,
	repository *devicessqlite.DeviceRepository,
	manifest Manifest,
	entityID devices.EntityID,
) error {
	observationCount, err := fixtureCount(ctx, database,
		"SELECT COUNT(*) FROM observations WHERE entity_id = ?", manifest.EntityID)
	if err != nil {
		return err
	}
	history, err := repository.ListEntityStateHistory(ctx, devices.ListEntityStateHistoryParams{
		EntityID: entityID, Filter: devices.EntityStateHistoryFilterAll, Limit: validationPageLimit,
	})
	if err != nil || int64(len(history.Items)) != minInt64(observationCount) {
		return fmt.Errorf("%w: decode Entity state history: %w", errFixtureDomain, err)
	}
	eventCount, err := fixtureCount(ctx, database,
		"SELECT COUNT(*) FROM entity_events WHERE entity_id = ?", manifest.EventEntityID)
	if err != nil {
		return err
	}
	events, err := repository.ListEntityEvents(ctx, devices.ListEntityEventsParams{
		EntityID: devices.EntityID(manifest.EventEntityID), Limit: validationPageLimit,
	})
	if err != nil || int64(len(events.Items)) != minInt64(eventCount) {
		return fmt.Errorf("%w: decode Entity Events: %w", errFixtureDomain, err)
	}
	return nil
}

func checkAutomationDecoding(ctx context.Context, database *sql.DB, manifest Manifest) error {
	repository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
	record, err := repository.GetAutomation(ctx, automations.AutomationID(manifest.AutomationID))
	if err != nil || record.ID != automations.AutomationID(manifest.AutomationID) ||
		record.Definition.Name == "" || record.Revision < 1 {
		return fmt.Errorf("%w: decode Automation: %w", errFixtureDomain, err)
	}
	historyCount, err := fixtureCount(ctx, database,
		"SELECT COUNT(*) FROM automation_history WHERE automation_id = ?", manifest.AutomationID)
	if err != nil {
		return err
	}
	history, err := repository.ListHistory(ctx, automations.ListHistoryParams{
		AutomationID: automations.AutomationID(manifest.AutomationID), Limit: validationPageLimit,
	})
	if err != nil || int64(len(history.Items)) != minInt64(historyCount) {
		return fmt.Errorf("%w: decode Automation history: %w", errFixtureDomain, err)
	}
	return checkAutomationRunDecoding(ctx, database, repository, manifest)
}

func checkAutomationRunDecoding(
	ctx context.Context,
	database *sql.DB,
	repository *automationssqlite.AutomationRepository,
	manifest Manifest,
) error {
	var runID string
	err := database.QueryRowContext(ctx, `
SELECT id FROM automation_history
WHERE automation_id = ? AND kind = 'run' AND run_status IN ('succeeded','failed','interrupted')
ORDER BY recorded_at DESC, id DESC LIMIT 1`, manifest.AutomationID).Scan(&runID)
	if err != nil {
		return fmt.Errorf("%w: read terminal Run: %w", errFixtureDomain, err)
	}
	entry, err := repository.GetHistoryEntry(ctx, automations.AutomationID(manifest.AutomationID), runID)
	if err != nil {
		return fmt.Errorf("%w: decode terminal Run: %w", errFixtureDomain, err)
	}
	run, ok := entry.(automations.Run)
	if !ok || len(run.Executions) == 0 ||
		automations.RunStateStatus(run.State) == automations.RunRunning {
		return fmt.Errorf("%w: terminal Run decoded incompletely", errFixtureDomain)
	}
	return nil
}

func checkNotificationDecoding(ctx context.Context, database *sql.DB, manifest Manifest) error {
	repository := notificationssqlite.NewRepository(database)
	destinations, err := repository.ListDestinations(ctx, validationPageLimit, "")
	if err != nil || len(destinations.Items) == 0 {
		return fmt.Errorf("%w: decode notification destinations: %w", errFixtureDomain, err)
	}
	destination, err := repository.GetDestination(
		ctx, notifications.DestinationID(manifest.DestinationID),
	)
	if err != nil || destination.ID != notifications.DestinationID(manifest.DestinationID) {
		return fmt.Errorf("%w: decode notification destination: %w", errFixtureDomain, err)
	}
	attemptCount, err := fixtureCount(ctx, database,
		"SELECT COUNT(*) FROM notification_attempts WHERE destination_id = ?", manifest.DestinationID)
	if err != nil {
		return err
	}
	attempts, err := repository.ListAttempts(ctx, notifications.AttemptFilter{
		DestinationID: notifications.DestinationID(manifest.DestinationID),
		Limit:         validationPageLimit,
	})
	if err != nil || int64(len(attempts.Items)) != minInt64(attemptCount) {
		return fmt.Errorf("%w: decode notification attempts: %w", errFixtureDomain, err)
	}
	return nil
}

func checkFunctionDecoding(ctx context.Context, database *sql.DB, manifest Manifest) error {
	repository := adapterssqlite.NewAdapterRepository(database)
	functions, err := repository.ListFunctions(ctx, manifest.AdapterID, adapters.PageInput{
		Limit: validationPageLimit,
	})
	if err != nil || len(functions.Items) == 0 {
		return fmt.Errorf("%w: decode Adapter functions: %w", errFixtureDomain, err)
	}
	invocationCount, err := fixtureCount(ctx, database,
		"SELECT COUNT(*) FROM adapter_function_invocations WHERE adapter_id = ?", manifest.AdapterID)
	if err != nil {
		return err
	}
	filter := adapters.InvocationFilter{AdapterID: manifest.AdapterID}
	filter.PageInput.Limit = validationPageLimit
	invocations, err := repository.ListInvocations(ctx, filter)
	if err != nil || int64(len(invocations.Items)) != minInt64(invocationCount) {
		return fmt.Errorf("%w: decode Adapter invocations: %w", errFixtureDomain, err)
	}
	return nil
}

func checkDashboardDecoding(ctx context.Context, database *sql.DB, manifest Manifest) error {
	repository := dashboardssqlite.NewRepository(database)
	dashboard, err := repository.GetDashboard(ctx, dashboards.DashboardID(manifest.DashboardID))
	if err != nil || string(dashboard.ID) != manifest.DashboardID || len(dashboard.Definition) == 0 {
		return fmt.Errorf("%w: decode dashboard: %w", errFixtureDomain, err)
	}
	return nil
}

func checkAgentDecoding(ctx context.Context, database *sql.DB, manifest Manifest) error {
	queries := agentsql.New(database)
	messages, err := queries.ListMessages(ctx, agentsql.ListMessagesParams{
		ConversationID: manifest.ConversationID,
	})
	if err != nil || len(messages) == 0 {
		return fmt.Errorf("%w: decode Agent messages: %w", errFixtureDomain, err)
	}
	for _, message := range messages {
		var decoded map[string]any
		if err = json.Unmarshal([]byte(message.MessageJson), &decoded); err != nil {
			return fmt.Errorf("%w: decode Agent message JSON: %w", errFixtureDomain, err)
		}
	}
	summaries, err := queries.ListConversationSummaries(ctx)
	if err != nil || int64(len(summaries)) == 0 {
		return fmt.Errorf("%w: decode Agent conversations: %w", errFixtureDomain, err)
	}
	return nil
}

func minInt64(value int64) int64 {
	if value < int64(validationPageLimit) {
		return value
	}
	return int64(validationPageLimit)
}
