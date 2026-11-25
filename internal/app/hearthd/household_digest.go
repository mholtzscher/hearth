package hearthd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

const (
	// digestPageLimit is the page size for full Device, Entity, and
	// Automation reads; it is the largest page Core serves.
	digestPageLimit = 200
	// digestHistoryLimit is how many of an Automation's newest history entries
	// the digest inspects for problems.
	digestHistoryLimit = 20
	// digestProblemWindow is how far back Automation problems are reported.
	digestProblemWindow = 24 * time.Hour
	// digestProblemsPerAutomation bounds the problems reported per Automation.
	digestProblemsPerAutomation = 3
)

// digestDeviceReads is the Devices capability the household digest needs.
type digestDeviceReads interface {
	ListDevices(ctx context.Context, params devices.ListDevicesParams) (devices.Page[devices.Device], error)
	ListEntities(ctx context.Context, params devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error)
}

// digestAutomationReads is the Automations capability the household digest needs.
type digestAutomationReads interface {
	ListAutomations(
		ctx context.Context,
		params automations.ListAutomationsParams,
	) (automations.Page[automations.Record], error)
	ListHistory(
		ctx context.Context,
		params automations.ListHistoryParams,
	) (automations.Page[automations.HistorySummary], error)
}

// householdDigestReader implements agent.HouseholdDigestReader over Devices
// and Automations, so the agent module does not import them.
type householdDigestReader struct {
	devices     digestDeviceReads
	presenter   *devices.Presenter
	automations digestAutomationReads
	location    *time.Location
	unitSystem  household.UnitSystem
	now         func() time.Time
}

func newHouseholdDigestReader(
	deviceReads digestDeviceReads,
	automationReads digestAutomationReads,
	settings household.Settings,
	location *time.Location,
) householdDigestReader {
	return householdDigestReader{
		devices:     deviceReads,
		presenter:   newDevicesPresenter(settings),
		automations: automationReads,
		location:    location,
		unitSystem:  settings.UnitSystem,
		now:         time.Now,
	}
}

// ReadHouseholdDigest reads every Device, Entity, and Automation. It omits
// disabled Entities and Entities whose availability is unknown, projects State
// to household units, and keeps failed or interrupted runs and skips with
// unknown conditions from the last 24 hours. Any read error fails the digest.
func (reader householdDigestReader) ReadHouseholdDigest(ctx context.Context) (agent.HouseholdDigest, error) {
	now := reader.now()
	entities, err := reader.readEntities(ctx)
	if err != nil {
		return agent.HouseholdDigest{}, err
	}
	automationDigest, err := reader.readAutomations(ctx, now)
	if err != nil {
		return agent.HouseholdDigest{}, err
	}
	return agent.HouseholdDigest{
		LocalTime:   now.In(reader.location),
		UnitSystem:  reader.unitSystem,
		Entities:    entities,
		Automations: automationDigest,
	}, nil
}

func (reader householdDigestReader) readEntities(ctx context.Context) ([]agent.DigestEntity, error) {
	deviceNames := make(map[devices.DeviceID]string)
	var afterDevice *devices.DeviceID
	for {
		page, err := reader.devices.ListDevices(
			ctx,
			devices.ListDevicesParams{AfterID: afterDevice, Limit: digestPageLimit},
		)
		if err != nil {
			return nil, fmt.Errorf("list devices: %w", err)
		}
		for _, device := range page.Items {
			deviceNames[device.ID] = effectiveName(device.Name, device.NameOverride)
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		afterDevice = &page.Items[len(page.Items)-1].ID
	}
	var entities []agent.DigestEntity
	var afterEntity *devices.EntityID
	for {
		page, err := reader.devices.ListEntities(
			ctx,
			devices.ListEntitiesParams{AfterID: afterEntity, Limit: digestPageLimit},
		)
		if err != nil {
			return nil, fmt.Errorf("list entities: %w", err)
		}
		for _, view := range page.Items {
			if entity, ok := reader.digestEntity(view, deviceNames); ok {
				entities = append(entities, entity)
			}
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		afterEntity = &page.Items[len(page.Items)-1].Entity.ID
	}
	return entities, nil
}

func (reader householdDigestReader) digestEntity(
	view devices.EntityWithState,
	deviceNames map[devices.DeviceID]string,
) (agent.DigestEntity, bool) {
	if !view.Entity.Enabled {
		return agent.DigestEntity{}, false
	}
	var availability string
	switch view.Availability.Status {
	case devices.EntityAvailabilityAvailable:
		availability = agent.DigestAvailable
	case devices.EntityAvailabilityUnavailable:
		availability = agent.DigestUnavailable
	case devices.EntityAvailabilityUnknown:
		return agent.DigestEntity{}, false
	default:
		return agent.DigestEntity{}, false
	}
	entity := agent.DigestEntity{
		Name:         effectiveName(view.Entity.Name, view.Entity.NameOverride),
		Device:       deviceNames[view.Entity.DeviceID],
		Type:         string(view.Entity.TypeID),
		Availability: availability,
	}
	// A State that cannot be projected is left out rather than failing the
	// digest; the Entity still counts and still reports availability.
	projected, metadata, err := reader.presenter.Entity(view, "")
	if err != nil || projected.State == nil {
		return entity, true
	}
	entity.State = roundQuantities(json.RawMessage(projected.State.Value), metadata)
	if len(metadata) > 0 {
		entity.Units = make(map[string]string, len(metadata))
		for path, quantity := range metadata {
			entity.Units[path] = quantity.Label
		}
	}
	return entity, true
}

// roundQuantities rounds numeric quantity fields to their display precision,
// so a converted 21.5 °C reads 70.7 rather than 70.69999999999999. It handles
// the root and top-level object fields; anything else is returned unchanged.
func roundQuantities(state json.RawMessage, metadata map[string]devices.QuantityMetadata) json.RawMessage {
	if quantity, ok := metadata[""]; ok {
		return roundNumber(state, quantity.FractionDigits)
	}
	var object map[string]json.RawMessage
	if len(metadata) == 0 || json.Unmarshal(state, &object) != nil {
		return state
	}
	for path, quantity := range metadata {
		name, ok := strings.CutPrefix(path, "/")
		if !ok || strings.Contains(name, "/") || object[name] == nil {
			continue
		}
		object[name] = roundNumber(object[name], quantity.FractionDigits)
	}
	rounded, err := json.Marshal(object)
	if err != nil {
		return state
	}
	return rounded
}

func roundNumber(raw json.RawMessage, digits int) json.RawMessage {
	var value float64
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	scale := math.Pow10(digits)
	rounded, err := json.Marshal(math.Round(value*scale) / scale)
	if err != nil {
		return raw
	}
	return rounded
}

func (reader householdDigestReader) readAutomations(
	ctx context.Context,
	now time.Time,
) ([]agent.DigestAutomation, error) {
	var digest []agent.DigestAutomation
	var after *automations.AutomationID
	for {
		page, err := reader.automations.ListAutomations(
			ctx, automations.ListAutomationsParams{AfterID: after, Limit: digestPageLimit},
		)
		if err != nil {
			return nil, fmt.Errorf("list automations: %w", err)
		}
		for _, record := range page.Items {
			problems, problemErr := reader.automationProblems(ctx, record.ID, now)
			if problemErr != nil {
				return nil, problemErr
			}
			digest = append(digest, agent.DigestAutomation{
				Name: record.Definition.Name, Enabled: record.Definition.Enabled, Problems: problems,
			})
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		after = &page.Items[len(page.Items)-1].ID
	}
	return digest, nil
}

func (reader householdDigestReader) automationProblems(
	ctx context.Context,
	id automations.AutomationID,
	now time.Time,
) ([]agent.DigestAutomationProblem, error) {
	page, err := reader.automations.ListHistory(
		ctx,
		automations.ListHistoryParams{AutomationID: id, Limit: digestHistoryLimit},
	)
	if err != nil {
		return nil, fmt.Errorf("list automation history: %w", err)
	}
	cutoff := now.Add(-digestProblemWindow)
	var problems []agent.DigestAutomationProblem
	for _, entry := range page.Items {
		if len(problems) == digestProblemsPerAutomation || entry.RecordedAt.Before(cutoff) {
			break
		}
		if kind := problemKind(entry.Body); kind != "" {
			problems = append(
				problems,
				agent.DigestAutomationProblem{At: entry.RecordedAt.In(reader.location), Kind: kind},
			)
		}
	}
	return problems, nil
}

func problemKind(body automations.HistorySummaryBody) string {
	switch summary := body.(type) {
	case automations.RunHistorySummary:
		switch summary.Status {
		case automations.RunFailed:
			return agent.DigestProblemFailed
		case automations.RunInterrupted:
			return agent.DigestProblemInterrupted
		case automations.RunRunning, automations.RunSucceeded:
			return ""
		default:
			return ""
		}
	case automations.SkipHistorySummary:
		if summary.Reason == automations.SkipConditionsUnknown {
			return agent.DigestProblemConditionsUnknown
		}
	}
	return ""
}

func effectiveName(name string, override *string) string {
	if override != nil && *override != "" {
		return *override
	}
	return name
}
