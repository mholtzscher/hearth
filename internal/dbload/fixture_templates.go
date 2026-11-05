//nolint:goconst,mnd // Template literals mirror the production JSON vocabulary and bounded value ranges.
package dbload

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

const (
	fixtureDefinitionSteps       = 2
	fixtureDefinitionTriggers    = 2
	fixtureConditionDecisionJSON = `{"mode":"not_configured","bypass_requested":false}`
	fixtureFunctionName          = "get_status"
	fixtureFunctionTimeoutMS     = 1000
)

// fixtureSupportJSON returns the canonical Entity support document for one
// fixture role. Records are normalized by the production Entity type catalog
// before use.
func fixtureSupportJSON(role int) json.RawMessage {
	switch role {
	case rolePower:
		return json.RawMessage(`{"operations":{"set":{}},"state":{}}`)
	case roleEvent:
		return json.RawMessage(
			`{"events":{"names":["double_press","long_press","single_press"]},"operations":{},"state":{}}`,
		)
	default:
		return json.RawMessage(`{"operations":{},"state":{}}`)
	}
}

func fixtureEntityType(role int) string {
	switch role {
	case rolePower:
		return "hearth.power/v1"
	case roleTemperature:
		return "hearth.temperature/v1"
	case roleHumidity:
		return "hearth.relativehumidity/v1"
	default:
		return "hearth.enumevent/v1"
	}
}

func fixtureEntityName(role int) string {
	switch role {
	case rolePower:
		return "Power"
	case roleTemperature:
		return "Temperature"
	case roleHumidity:
		return "Humidity"
	default:
		return "Event"
	}
}

// fixtureStateValue renders the deterministic State value for one role and
// history index. Values remain inside each Entity type's documented bounds.
func fixtureStateValue(role int, index int) json.RawMessage {
	switch role {
	case rolePower:
		if index%2 == 0 {
			return json.RawMessage(`true`)
		}
		return json.RawMessage(`false`)
	case roleTemperature:
		return json.RawMessage(strconv.Itoa(20000 + index%4000))
	case roleHumidity:
		return json.RawMessage(fmt.Sprintf("%d.%d", 40+index%20, index%10))
	default:
		return json.RawMessage(`{}`)
	}
}

// fixtureAutomationDefinition builds one strict automation definition with two
// triggers and two steps. The production definition codec must accept it.
func fixtureAutomationDefinition(index int, powerEntityID string) (json.RawMessage, error) {
	definition := map[string]any{
		"name":    fmt.Sprintf("Fixture Automation %d", index),
		"enabled": true,
		"triggers": []any{
			map[string]any{
				"id":           "state",
				"kind":         "observation",
				"entity_id":    powerEntityID,
				"dispositions": []any{"applied"},
				"comparisons": []any{
					map[string]any{"value_pointer": "", "operator": "eq", "operand": true},
				},
			},
			map[string]any{"id": "clock", "kind": "cron", "expression": "0 7 * * *"},
		},
		"steps": []any{
			map[string]any{
				"id": "command", "kind": "command", "entity_id": powerEntityID, "operation": "set",
				"parameters": map[string]any{"value": true},
			},
			map[string]any{"id": "wait", "kind": "delay", "duration_ms": 1000},
		},
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("encode automation definition: %w", err)
	}
	if _, err = automations.DecodeDefinition(raw); err != nil {
		return nil, fmt.Errorf("validate automation definition: %w", err)
	}
	return raw, nil
}

// fixtureDashboardDefinition builds one saved dashboard bound to a real
// temperature Entity. The production view codec must accept it.
func fixtureDashboardDefinition(index int, temperatureEntityID string) (json.RawMessage, error) {
	spec := map[string]any{
		"root": "stack",
		"elements": map[string]any{
			"stack": map[string]any{
				"type": "Stack", "props": map[string]any{"gap": "small"}, "children": []any{"temperature"},
			},
			"temperature": map[string]any{
				"type": "Metric",
				"props": map[string]any{
					"source": map[string]any{
						"kind":        "entity-state",
						"entity_id":   temperatureEntityID,
						"entity_type": "hearth.temperature/v1",
					},
				},
			},
		},
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("encode dashboard spec: %w", err)
	}
	definition := dashboards.Definition{
		SchemaVersion:  1,
		CatalogVersion: "hearth.dashboard/v1",
		Title:          fmt.Sprintf("Fixture Dashboard %d", index),
		Spec:           specJSON,
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("encode dashboard definition: %w", err)
	}
	if _, err = dashboards.ParseDefinition(raw); err != nil {
		return nil, fmt.Errorf("validate dashboard definition: %w", err)
	}
	return raw, nil
}

// fixtureMessageJSON encodes one Eino message through the same codec Core uses
// for retained Agent history.
func fixtureMessageJSON(role schema.RoleType, content string) ([]byte, error) {
	message := schema.Message{Role: role, Content: content}
	raw, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode agent message: %w", err)
	}
	return raw, nil
}

// fixtureFunctionCatalog prepares one adapter function catalog through the
// production catalog validation boundary.
func fixtureFunctionCatalog(
	adapterIndex int,
	adapterID string,
	runtimeID string,
) (adapters.Catalog, []byte, error) {
	inputSchema := json.RawMessage(
		`{"additionalProperties":false,"properties":{"entity_id":{"type":"string"}},"required":["entity_id"],"type":"object"}`,
	)
	outputSchema := json.RawMessage(
		`{"additionalProperties":false,"properties":{"ok":{"type":"boolean"}},"required":["ok"],"type":"object"}`,
	)
	catalog, err := adapters.PrepareCatalog(adapters.Catalog{
		ID:        adapters.CatalogID(fixtureID("afc_", fixtureIndex(adapterIndex))),
		AdapterID: adapterID,
		RuntimeID: adapters.RuntimeID(runtimeID),
		Functions: []adapters.Descriptor{
			{
				Key: fixtureFunctionName, Description: "Reports fixture status.",
				InputSchema: inputSchema, OutputSchema: outputSchema, TimeoutMS: fixtureFunctionTimeoutMS,
			},
			{
				Key: "set_mode", Description: "Sets fixture mode.",
				InputSchema: inputSchema, OutputSchema: outputSchema, TimeoutMS: fixtureFunctionTimeoutMS,
			},
		},
	})
	if err != nil {
		return adapters.Catalog{}, nil, fmt.Errorf("prepare function catalog: %w", err)
	}
	body, err := json.Marshal(catalog.Functions)
	if err != nil {
		return adapters.Catalog{}, nil, fmt.Errorf("encode function catalog: %w", err)
	}
	return catalog, body, nil
}

// fixtureHealthSnapshot encodes canonical Adapter health evidence through the
// production snapshot encoder.
func fixtureHealthSnapshot(
	adapterID string,
	runtimeID string,
	revision int64,
	at time.Time,
) ([]byte, error) {
	runtime := adapters.RuntimeID(runtimeID)
	observed := at
	body, err := adapters.EncodeHealthSnapshot(adapters.HealthSnapshot{
		AdapterID:        adapterID,
		RuntimeID:        &runtime,
		Revision:         adapters.HealthRevision(revision),
		Status:           adapters.AdapterHealthHealthy,
		Source:           "adapter",
		SourceObservedAt: &observed,
		ChangedAt:        at,
		EvidenceAt:       at,
	})
	if err != nil {
		return nil, fmt.Errorf("encode health snapshot: %w", err)
	}
	return body, nil
}
