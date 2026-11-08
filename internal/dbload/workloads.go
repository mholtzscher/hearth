package dbload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	agentsql "github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	automationssql "github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	dashboardssqlite "github.com/mholtzscher/hearth/internal/modules/dashboards/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationssqlite "github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
)

const (
	classBoundedRead   = "bounded-read"
	classUnboundedRead = "unbounded-read"
	classWrite         = "write"
	classMaintenance   = "maintenance"

	queryDevicesGetEntity            = "devices/GetEntity"
	queryDevicesListEntitiesByDevice = "devices/ListEntitiesByDevice"
	queryDevicesListDevices          = "devices/ListDevices"
	queryNotificationsListAttempts   = "notifications/ListAttempts"

	midpointDivisor         = 2
	snapshotEntryCount      = 2
	observationSequenceBase = 100_000_000
)

// Workload invokes an actual application persistence operation and checks its work.
type Workload struct {
	Name     string
	Class    string   // bounded-read, unbounded-read, write, maintenance
	Queries  []string // owner/name; branch-specific, not inferred from an SQL plan
	PlanArgs map[string][]any
	Mutates  bool
	Once     bool // destructive/recovery: one genuine operation per fresh block
	Run      func(context.Context, int) (int64, error)
}

type workloadBuilder struct {
	db *sql.DB
	m  Manifest
	d  *devicessqlite.DeviceRepository
	a  *adapterssqlite.AdapterRepository
	u  *automationssqlite.AutomationRepository
	n  *notificationssqlite.Repository
	h  *dashboardssqlite.Repository
	g  *agentsql.Queries
}

// Workloads prepares independent count/cursor oracles outside measured regions.
func Workloads(ctx context.Context, database *sql.DB, m Manifest) ([]Workload, error) {
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		return nil, err
	}
	b := workloadBuilder{
		db: database,
		m:  m,
		d:  devicessqlite.NewDeviceRepository(database, catalog),
		a:  adapterssqlite.NewAdapterRepository(database),
		u:  automationssqlite.NewAutomationRepository(database, automations.Dependencies{}),
		n:  notificationssqlite.NewRepository(database),
		h:  dashboardssqlite.NewRepository(database),
		g:  agentsql.New(database),
	}
	var result []Workload
	for _, build := range []func(context.Context) ([]Workload, error){b.inventory, b.history, b.management, b.agent, b.writes, b.numericWrites} {
		items, buildErr := build(ctx)
		if buildErr != nil {
			return nil, buildErr
		}
		result = append(result, items...)
	}
	return result, nil
}

func checkedWork(got, want int64, err error) (int64, error) {
	if err != nil {
		return got, err
	}
	if got != want {
		return got, fmt.Errorf("incorrect work: got %d rows/effects, want %d", got, want)
	}
	return got, nil
}

func (b workloadBuilder) count(ctx context.Context, query string, args ...any) (int64, error) {
	var n int64
	err := b.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func (b workloadBuilder) inventory(ctx context.Context) ([]Workload, error) {
	var after string
	if err := b.db.QueryRowContext(ctx, "SELECT id FROM devices ORDER BY id LIMIT 1 OFFSET ?", b.m.RowCounts["devices"]/midpointDivisor).
		Scan(&after); err != nil {
		return nil, err
	}
	countByDevice, err := b.count(ctx, "SELECT count(*) FROM entities WHERE device_id=?", b.m.DeviceID)
	if err != nil {
		return nil, err
	}
	return b.inventoryWorkloads(after, countByDevice), nil
}

func (b workloadBuilder) inventoryWorkloads(after string, countByDevice int64) []Workload {
	device := devices.DeviceID(b.m.DeviceID)
	entity := devices.EntityID(b.m.EntityID)
	return []Workload{
		{
			Name:     "devices.get-entity",
			Class:    classBoundedRead,
			Queries:  []string{queryDevicesGetEntity},
			PlanArgs: map[string][]any{queryDevicesGetEntity: {b.m.EntityID}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.GetEntity(ctx, entity)
				if e == nil && (v.Entity.ID != entity || v.State == nil) {
					e = errors.New("wrong Entity or missing current State")
				}
				return checkedWork(1, 1, e)
			},
		},
		{
			Name:    "devices.get-device",
			Class:   classBoundedRead,
			Queries: []string{"devices/GetDevice", queryDevicesListEntitiesByDevice},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.GetDevice(ctx, devices.GetDeviceParams{ID: device, EntityLimit: DefaultPageSize})
				if e == nil && v.Device.ID != device {
					e = errors.New("wrong Device")
				}
				return checkedWork(int64(len(v.Entities.Items)), min(countByDevice, DefaultPageSize), e)
			},
		},
		{
			Name:     "devices.list-devices.first",
			Class:    classBoundedRead,
			Queries:  []string{queryDevicesListDevices},
			PlanArgs: map[string][]any{queryDevicesListDevices: {"", DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.ListDevices(ctx, devices.ListDevicesParams{Limit: DefaultPageSize})
				return checkedWork(int64(len(v.Items)), min(b.m.RowCounts["devices"], DefaultPageSize), e)
			},
		},
		{
			Name:     "devices.list-devices.deep",
			Class:    classBoundedRead,
			Queries:  []string{queryDevicesListDevices},
			PlanArgs: map[string][]any{queryDevicesListDevices: {after, DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				id := devices.DeviceID(after)
				v, e := b.d.ListDevices(ctx, devices.ListDevicesParams{AfterID: &id, Limit: DefaultPageSize})
				if e == nil && len(v.Items) > 0 && string(v.Items[0].ID) <= after {
					e = errors.New("deep cursor did not advance")
				}
				return checkedWork(
					int64(len(v.Items)),
					min(b.m.RowCounts["devices"]-b.m.RowCounts["devices"]/midpointDivisor-1, DefaultPageSize),
					e,
				)
			},
		},
		{
			Name:     "devices.list-entities.first",
			Class:    classBoundedRead,
			Queries:  []string{"devices/ListEntities"},
			PlanArgs: map[string][]any{"devices/ListEntities": {"", DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.ListEntities(ctx, devices.ListEntitiesParams{Limit: DefaultPageSize})
				return checkedWork(int64(len(v.Items)), min(b.m.RowCounts["entities"], DefaultPageSize), e)
			},
		},
		{
			Name:     "devices.list-entities.by-device",
			Class:    classBoundedRead,
			Queries:  []string{queryDevicesListEntitiesByDevice},
			PlanArgs: map[string][]any{queryDevicesListEntitiesByDevice: {b.m.DeviceID, "", DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.ListEntities(ctx, devices.ListEntitiesParams{DeviceID: &device, Limit: DefaultPageSize})
				for _, x := range v.Items {
					if x.Entity.DeviceID != device {
						e = errors.New("cross-Device Entity")
					}
				}
				return checkedWork(int64(len(v.Items)), min(countByDevice, DefaultPageSize), e)
			},
		},
		{
			Name:    "devices.state-snapshot",
			Class:   classBoundedRead,
			Queries: []string{"devices/GetEntityStateSnapshot"},
			PlanArgs: map[string][]any{
				"devices/GetEntityStateSnapshot": {fmt.Sprintf("[%q,%q]", b.m.EntityID, b.m.EventEntityID)},
			},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.GetEntityStateSnapshot(ctx, []devices.EntityID{entity, devices.EntityID(b.m.EventEntityID)})
				return checkedWork(int64(len(v.Entries)), snapshotEntryCount, e)
			},
		},
	}
}

func (b workloadBuilder) history(ctx context.Context) ([]Workload, error) {
	entity := devices.EntityID(b.m.EntityID)
	pages, err := b.historyPageWorkloads(ctx, entity)
	if err != nil {
		return nil, err
	}
	rollups, err := b.historyRollupWorkloads(ctx)
	if err != nil {
		return nil, err
	}
	events, err := b.entityEventsWorkload(ctx)
	if err != nil {
		return nil, err
	}
	pages = append(pages, rollups...)
	return append(pages, events...), nil
}

func (b workloadBuilder) historyPageWorkloads(ctx context.Context, entity devices.EntityID) ([]Workload, error) {
	var cursor int64
	if err := b.db.QueryRowContext(ctx, "SELECT receive_order FROM observations WHERE entity_id=? ORDER BY receive_order DESC LIMIT 1 OFFSET ?", b.m.EntityID, b.m.HistoryRows/midpointDivisor).
		Scan(&cursor); err != nil {
		return nil, err
	}
	var result []Workload
	for _, filter := range []devices.EntityStateHistoryFilter{devices.EntityStateHistoryFilterAll, devices.EntityStateHistoryFilterUpdates, devices.EntityStateHistoryFilterApplied, devices.EntityStateHistoryFilterUnchanged, devices.EntityStateHistoryFilterRejected} {
		for _, deep := range []bool{false, true} {
			item, err := b.historyPageWorkload(ctx, entity, filter, deep, cursor)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
	}
	return result, nil
}

func (b workloadBuilder) historyPageWorkload(
	ctx context.Context,
	entity devices.EntityID,
	filter devices.EntityStateHistoryFilter,
	deep bool,
	cursor int64,
) (Workload, error) {
	params := devices.ListEntityStateHistoryParams{EntityID: entity, Filter: filter, Limit: DefaultPageSize}
	query := "SELECT count(*) FROM observations WHERE entity_id=?"
	args := []any{b.m.EntityID}
	name := "devices.state-history." + string(filter) + ".first"
	qname := "ListEntityStateHistory"
	before := int64(math.MaxInt64)
	if deep {
		params.BeforeReceiveOrder = &cursor
		query += " AND receive_order<?"
		args = append(args, cursor)
		name = "devices.state-history." + string(filter) + ".deep"
		before = cursor
	}
	plan := []any{b.m.EntityID, before, DefaultPageSize + 1}
	if filter == devices.EntityStateHistoryFilterUpdates {
		query += " AND disposition IN ('applied','unchanged')"
		qname = "ListEntityStateUpdates"
	} else if filter != devices.EntityStateHistoryFilterAll {
		query += " AND disposition=?"
		args = append(args, string(filter))
		qname = "ListEntityStateHistoryByDisposition"
		plan = []any{b.m.EntityID, string(filter), before, DefaultPageSize + 1}
	}
	n, err := b.count(ctx, query, args...)
	if err != nil {
		return Workload{}, err
	}
	want := min(n, DefaultPageSize)
	return Workload{
		Name:     name,
		Class:    classBoundedRead,
		Queries:  []string{"devices/" + qname},
		PlanArgs: map[string][]any{"devices/" + qname: plan},
		Run: func(ctx context.Context, _ int) (int64, error) {
			v, readErr := b.d.ListEntityStateHistory(ctx, params)
			for i, x := range v.Items {
				if (i > 0 && x.ReceiveOrder >= v.Items[i-1].ReceiveOrder) ||
					(params.BeforeReceiveOrder != nil && x.ReceiveOrder >= *params.BeforeReceiveOrder) {
					readErr = errors.New("history order or cursor violation")
				}
			}
			return checkedWork(int64(len(v.Items)), want, readErr)
		},
	}, nil
}

// historyRollupWorkloads read dashboard-shaped numeric summaries: a 24h window
// in 5-minute buckets and a 7d window in 30-minute buckets, from rollup cells.
func (b workloadBuilder) historyRollupWorkloads(ctx context.Context) ([]Workload, error) {
	var entity string
	if err := b.db.QueryRowContext(ctx,
		"SELECT id FROM entities WHERE type_id = ? ORDER BY id LIMIT 1", string(devices.EntityTypeTemperatureV1),
	).Scan(&entity); err != nil {
		return nil, fmt.Errorf("select rollup entity: %w", err)
	}
	var result []Workload
	for _, shape := range []struct {
		name           string
		window, bucket time.Duration
	}{
		{"24h", 24 * time.Hour, 5 * time.Minute},
		{"7d", 7 * 24 * time.Hour, 30 * time.Minute},
	} {
		end := b.m.Clock.Truncate(devices.HistoryRollupCellDuration)
		request := devices.HistoryRollupRequest{
			EntityID: devices.EntityID(entity), Field: "",
			From: end.Add(-shape.window), End: end, Bucket: shape.bucket,
		}
		buckets, err := b.count(ctx, `SELECT count(DISTINCT (cell_start - ?) / ?) FROM observation_rollups
			WHERE entity_id = ? AND field_path = '' AND cell_start >= ? AND cell_start < ?`,
			request.From.Unix(), int64(shape.bucket/time.Second), entity, request.From.Unix(), end.Unix())
		if err != nil {
			return nil, err
		}
		result = append(result, Workload{
			Name:    "devices.history-rollups." + shape.name,
			Class:   classBoundedRead,
			Queries: []string{"devices/HistoryEntityType", "devices/HistoryRollupCells"},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.ReadHistoryRollupBuckets(ctx, request)
				return checkedWork(int64(len(v)), buckets, e)
			},
		})
	}
	return result, nil
}

func (b workloadBuilder) entityEventsWorkload(ctx context.Context) ([]Workload, error) {
	events, err := b.count(ctx, "SELECT count(*) FROM entity_events WHERE entity_id=?", b.m.EventEntityID)
	if err != nil {
		return nil, err
	}
	return []Workload{
		{
			Name:    "devices.entity-events.first",
			Class:   classBoundedRead,
			Queries: []string{"devices/ListEntityEvents"},
			PlanArgs: map[string][]any{
				"devices/ListEntityEvents": {b.m.EventEntityID, int64(math.MaxInt64), DefaultPageSize + 1},
			},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.ListEntityEvents(
					ctx,
					devices.ListEntityEventsParams{
						EntityID: devices.EntityID(b.m.EventEntityID),
						Limit:    DefaultPageSize,
					},
				)
				return checkedWork(int64(len(v.Items)), min(events, DefaultPageSize), e)
			},
		},
	}, nil
}

func (b workloadBuilder) management(ctx context.Context) ([]Workload, error) {
	entity := devices.EntityID(b.m.EntityID)
	commandCount, err := b.count(ctx, "SELECT count(*) FROM commands WHERE entity_id=?", b.m.EntityID)
	if err != nil {
		return nil, err
	}
	result := b.managementWorkloads(entity, commandCount)
	statuses, err := b.commandStatusWorkloads(ctx)
	if err != nil {
		return nil, err
	}
	result = append(result, statuses...)
	history, err := b.automationHistoryWorkload(ctx)
	if err != nil {
		return nil, err
	}
	result = append(result, history...)
	attempts, err := b.notificationAttemptWorkload(ctx)
	if err != nil {
		return nil, err
	}
	result = append(result, attempts...)
	invocations, err := b.functionInvocationWorkload(ctx)
	if err != nil {
		return nil, err
	}
	result = append(result, invocations...)
	return result, nil
}

func (b workloadBuilder) managementWorkloads(entity devices.EntityID, commandCount int64) []Workload {
	return []Workload{
		{
			Name:     "devices.commands.global",
			Class:    classBoundedRead,
			Queries:  []string{"devices/ListCommandsFirstPage"},
			PlanArgs: map[string][]any{"devices/ListCommandsFirstPage": {DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.ListCommands(ctx, devices.ListCommandsParams{Limit: DefaultPageSize})
				return checkedWork(int64(len(v.Items)), min(b.m.RowCounts["commands"], DefaultPageSize), e)
			},
		},
		{
			Name:     "devices.commands.by-entity",
			Class:    classBoundedRead,
			Queries:  []string{"devices/ListCommandsByEntityFirstPage"},
			PlanArgs: map[string][]any{"devices/ListCommandsByEntityFirstPage": {b.m.EntityID, DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.d.ListCommands(ctx, devices.ListCommandsParams{EntityID: &entity, Limit: DefaultPageSize})
				return checkedWork(int64(len(v.Items)), min(commandCount, DefaultPageSize), e)
			},
		},
		{
			Name:     "adapters.list",
			Class:    classBoundedRead,
			Queries:  []string{"adapters/ListAdapterViewsFirstPage"},
			PlanArgs: map[string][]any{"adapters/ListAdapterViewsFirstPage": {DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.a.ListAdapters(ctx, adapters.ListAdaptersParams{Limit: DefaultPageSize})
				return checkedWork(int64(len(v.Items)), min(b.m.RowCounts["adapter_instances"], DefaultPageSize), e)
			},
		},
		{
			Name:     "automations.list",
			Class:    classBoundedRead,
			Queries:  []string{"automations/ListAutomationsFirstPage"},
			PlanArgs: map[string][]any{"automations/ListAutomationsFirstPage": {DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.u.ListAutomations(ctx, automations.ListAutomationsParams{Limit: DefaultPageSize})
				return checkedWork(int64(len(v.Items)), min(b.m.RowCounts["automations"], DefaultPageSize), e)
			},
		},
		{
			Name:     "notifications.destinations",
			Class:    classBoundedRead,
			Queries:  []string{"notifications/ListDestinations"},
			PlanArgs: map[string][]any{"notifications/ListDestinations": {"", DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.n.ListDestinations(ctx, DefaultPageSize, "")
				return checkedWork(
					int64(len(v.Items)),
					min(b.m.RowCounts["notification_destinations"], DefaultPageSize),
					e,
				)
			},
		},
		{
			Name:     "notifications.attempts.global",
			Class:    classBoundedRead,
			Queries:  []string{queryNotificationsListAttempts},
			PlanArgs: map[string][]any{queryNotificationsListAttempts: {"", "", "", DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.n.ListAttempts(ctx, notifications.AttemptFilter{Limit: DefaultPageSize})
				return checkedWork(int64(len(v.Items)), min(b.m.RowCounts["notification_attempts"], DefaultPageSize), e)
			},
		},
		{
			Name:     "dashboards.list",
			Class:    classBoundedRead,
			Queries:  []string{"dashboards/ListDashboards"},
			PlanArgs: map[string][]any{"dashboards/ListDashboards": {"", DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.h.ListDashboards(ctx, dashboards.ListParams{Limit: DefaultPageSize})
				return checkedWork(int64(len(v.Items)), min(b.m.RowCounts["dashboards"], DefaultPageSize), e)
			},
		},
	}
}

func (b workloadBuilder) commandStatusWorkloads(ctx context.Context) ([]Workload, error) {
	var result []Workload
	for _, status := range []devices.CommandStatus{devices.CommandStatusSatisfied, devices.CommandStatusRejected, devices.CommandStatusRequested} {
		n, e := b.count(ctx, "SELECT count(*) FROM commands WHERE status=?", string(status))
		if e != nil {
			return nil, e
		}
		result = append(
			result,
			Workload{
				Name:    "devices.commands.status-" + string(status),
				Class:   classBoundedRead,
				Queries: []string{"devices/ListCommandsByStatusFirstPage"},
				PlanArgs: map[string][]any{
					"devices/ListCommandsByStatusFirstPage": {string(status), DefaultPageSize + 1},
				},
				Run: func(ctx context.Context, _ int) (int64, error) {
					v, readErr := b.d.ListCommands(
						ctx,
						devices.ListCommandsParams{Status: &status, Limit: DefaultPageSize},
					)
					for _, x := range v.Items {
						if x.Status != status {
							readErr = errors.New("wrong Command status")
						}
					}
					return checkedWork(int64(len(v.Items)), min(n, DefaultPageSize), readErr)
				},
			},
		)
	}
	return result, nil
}

func (b workloadBuilder) automationHistoryWorkload(ctx context.Context) ([]Workload, error) {
	n, err := b.count(ctx, "SELECT count(*) FROM automation_history WHERE automation_id=?", b.m.AutomationID)
	if err != nil {
		return nil, err
	}
	return []Workload{
		{
			Name:     "automations.history.first",
			Class:    classBoundedRead,
			Queries:  []string{"automations/ListHistoryFirstPage"},
			PlanArgs: map[string][]any{"automations/ListHistoryFirstPage": {b.m.AutomationID, DefaultPageSize + 1}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.u.ListHistory(
					ctx,
					automations.ListHistoryParams{
						AutomationID: automations.AutomationID(b.m.AutomationID),
						Limit:        DefaultPageSize,
					},
				)
				return checkedWork(int64(len(v.Items)), min(n, DefaultPageSize), e)
			},
		},
	}, nil
}

func (b workloadBuilder) notificationAttemptWorkload(ctx context.Context) ([]Workload, error) {
	nDest, err := b.count(ctx, "SELECT count(*) FROM notification_attempts WHERE destination_id=?", b.m.DestinationID)
	if err != nil {
		return nil, err
	}
	return []Workload{
		{
			Name:    "notifications.attempts.by-destination",
			Class:   classBoundedRead,
			Queries: []string{queryNotificationsListAttempts},
			PlanArgs: map[string][]any{
				queryNotificationsListAttempts: {b.m.DestinationID, "", "", DefaultPageSize + 1},
			},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.n.ListAttempts(
					ctx,
					notifications.AttemptFilter{
						DestinationID: notifications.DestinationID(b.m.DestinationID),
						Limit:         DefaultPageSize,
					},
				)
				return checkedWork(int64(len(v.Items)), min(nDest, DefaultPageSize), e)
			},
		},
	}, nil
}

func (b workloadBuilder) functionInvocationWorkload(ctx context.Context) ([]Workload, error) {
	functionCount, err := b.count(
		ctx,
		"SELECT count(*) FROM adapter_function_invocations WHERE adapter_id=?",
		b.m.AdapterID,
	)
	if err != nil {
		return nil, err
	}
	return []Workload{
		{
			Name:    "adapters.function-invocations",
			Class:   classBoundedRead,
			Queries: []string{"adapters/GetAdapterInstance", "adapters/ListFunctionInvocations"},
			PlanArgs: map[string][]any{
				"adapters/ListFunctionInvocations": {b.m.AdapterID, "", "", "", DefaultPageSize + 1},
			},
			Run: func(ctx context.Context, _ int) (int64, error) {
				var filter adapters.InvocationFilter
				filter.AdapterID = b.m.AdapterID
				filter.Limit = DefaultPageSize
				v, e := b.a.ListInvocations(ctx, filter)
				return checkedWork(int64(len(v.Items)), min(functionCount, DefaultPageSize), e)
			},
		},
	}, nil
}

func (b workloadBuilder) agent(ctx context.Context) ([]Workload, error) {
	count, err := b.count(ctx, "SELECT count(*) FROM agent_messages WHERE conversation_id=?", b.m.ConversationID)
	if err != nil {
		return nil, err
	}
	return []Workload{
		{
			Name:     "agent.conversation-summaries",
			Class:    classUnboundedRead,
			Queries:  []string{"agent/ListConversationSummaries"},
			PlanArgs: map[string][]any{"agent/ListConversationSummaries": {}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.g.ListConversationSummaries(ctx)
				var messages int64
				for _, x := range v {
					messages += x.MessageCount
					if x.MessageCount > 0 && (!x.FirstUserMessageJson.Valid || !x.LastMessageAt.Valid) {
						e = errors.New("missing conversation evidence")
					}
				}
				if messages != b.m.RowCounts["agent_messages"] {
					e = errors.New("wrong summary message total")
				}
				return checkedWork(int64(len(v)), b.m.RowCounts["agent_conversations"], e)
			},
		},
		{
			Name:     "agent.messages.all",
			Class:    classUnboundedRead,
			Queries:  []string{"agent/ListMessages"},
			PlanArgs: map[string][]any{"agent/ListMessages": {b.m.ConversationID}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.g.ListMessages(ctx, agentsql.ListMessagesParams{ConversationID: b.m.ConversationID})
				return checkedWork(int64(len(v)), count, e)
			},
		},
		{
			Name:     "agent.messages.recent",
			Class:    classBoundedRead,
			Queries:  []string{"agent/ListRecentMessageJson"},
			PlanArgs: map[string][]any{"agent/ListRecentMessageJson": {b.m.ConversationID, DefaultPageSize}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := b.g.ListRecentMessageJson(
					ctx,
					agentsql.ListRecentMessageJsonParams{ConversationID: b.m.ConversationID, Limit: DefaultPageSize},
				)
				return checkedWork(int64(len(v)), min(count, DefaultPageSize), e)
			},
		},
		{
			Name:     "automations.enabled-scan.sql",
			Class:    classUnboundedRead,
			Queries:  []string{"automations/ListEnabledAutomations"},
			PlanArgs: map[string][]any{"automations/ListEnabledAutomations": {}},
			Run: func(ctx context.Context, _ int) (int64, error) {
				v, e := automationssql.New(b.db).ListEnabledAutomations(ctx)
				var enabled int64
				for _, x := range v {
					if x.DefinitionJson == "" {
						e = errors.New("empty enabled definition")
					}
					enabled++
				}
				return checkedWork(enabled, b.m.RowCounts["automations"], e)
			},
		},
	}, nil
}

//nolint:funlen // This is one table of complete write and maintenance operation definitions.
func (b workloadBuilder) writes(_ context.Context) ([]Workload, error) {
	return []Workload{
		{
			Name:    "devices.observation.append",
			Class:   classWrite,
			Mutates: true,
			Queries: []string{
				"devices/GetObservation",
				"devices/GetAdapterRuntime",
				queryDevicesGetEntity,
				"devices/InsertObservation",
				"devices/GetActiveAdapterRuntime",
				"devices/UpsertEntityState",
				"devices/InsertObservationDeviceFact",
			},
			Run: func(ctx context.Context, sequence int) (int64, error) {
				at := b.m.Clock.Add(time.Duration(sequence) * time.Millisecond)
				value := devices.Value("true")
				if sequence%2 == 0 {
					value = devices.Value("false")
				}
				v, e := b.d.ProjectObservation(
					ctx,
					devices.ProjectObservationParams{
						AdapterID: b.m.AdapterID,
						RuntimeID: adapters.RuntimeID(b.m.RuntimeID),
						Observation: devices.Observation{
							ID: devices.ObservationID(
								fmt.Sprintf("obs_019a4a00-0000-7000-8000-%012x", sequence+observationSequenceBase),
							),
							EntityID:          devices.EntityID(b.m.EntityID),
							Value:             value,
							CorrelationID:     devices.CorrelationID("cor_019a4a00-0000-7000-8000-000000000001"),
							AdapterReceivedAt: at,
						},
						ObservedAt: at,
						Now:        func() time.Time { return at },
					},
				)
				if e == nil &&
					(v.Disposition == devices.DispositionDuplicate || v.Disposition == devices.DispositionRejected || v.State == nil || v.PendingFactID == nil) {
					e = errors.New("append did not project State and enqueue fact")
				}
				return checkedWork(1, 1, e)
			},
		},
		{
			Name:    "devices.observations.prune",
			Class:   classMaintenance,
			Mutates: true,
			Once:    true,
			Queries: []string{"devices/DeleteExpiredObservations"},
			PlanArgs: map[string][]any{
				"devices/DeleteExpiredObservations": {
					b.m.Clock.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05.000000000Z"),
				},
			},
			Run: func(ctx context.Context, _ int) (int64, error) {
				return 1, b.d.DeleteExpiredObservations(ctx, b.m.Clock.Add(-8*24*time.Hour))
			},
		},
		{
			Name:    "devices.events.prune-batch",
			Class:   classMaintenance,
			Mutates: true,
			Once:    true,
			Queries: []string{"devices/DeleteEntityEventsBefore"},
			Run: func(ctx context.Context, _ int) (int64, error) {
				return b.d.DeleteEntityEventsBefore(ctx, b.m.Clock, pruneBatchSize)
			},
		},
		{
			Name:    "automations.history.prune-batch",
			Class:   classMaintenance,
			Mutates: true,
			Once:    true,
			Queries: []string{"automations/DeleteHistoryBefore", "automations/DeleteUnreferencedDefinitionRevisions"},
			Run: func(ctx context.Context, _ int) (int64, error) {
				return b.u.DeleteHistoryBefore(ctx, b.m.Clock, pruneBatchSize)
			},
		},
		{
			Name:    "notifications.history.prune-batch",
			Class:   classMaintenance,
			Mutates: true,
			Once:    true,
			Queries: []string{"notifications/DeleteHistoryBefore"},
			Run: func(ctx context.Context, _ int) (int64, error) {
				return b.n.DeleteHistoryBefore(ctx, b.m.Clock, pruneBatchSize)
			},
		},
		{
			Name:    "adapters.functions.prune-batch",
			Class:   classMaintenance,
			Mutates: true,
			Once:    true,
			Queries: []string{"adapters/DeleteFunctionHistoryBefore"},
			Run: func(ctx context.Context, _ int) (int64, error) {
				return b.a.DeleteHistoryBefore(ctx, b.m.Clock, pruneBatchSize)
			},
		},
	}, nil
}
