package hearthd //nolint:testpackage // Exercises the production catalog through its built-in agent bridge.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type journeyDevices struct {
	*stubDevices

	entity       devices.EntityWithState
	command      devices.CommandRecord
	lists        atomic.Int32
	commandReads atomic.Int32
}

func (s *journeyDevices) ListEntities(
	_ context.Context,
	in devices.ListEntitiesParams,
) (devices.Page[devices.EntityWithState], error) {
	if in.Limit != 50 || in.DeviceID != nil || in.AfterID != nil {
		return devices.Page[devices.EntityWithState]{}, fmt.Errorf("unexpected list input: %+v", in)
	}
	s.lists.Add(1)
	return devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{s.entity}}, nil
}

func (s *journeyDevices) GetCommand(_ context.Context, id devices.CommandID) (devices.CommandRecord, error) {
	if id != s.command.ID {
		return devices.CommandRecord{}, fmt.Errorf("wrong command identity: %s", id)
	}
	s.commandReads.Add(1)
	return s.command, nil
}

//nolint:gocognit // Keeps the scripted list-detail-receipt-history journey and exact call counts together.
func TestAgentBridgeDiscoversInspectsExecutesOnceAndReadsReceiptIdentity(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	observationID := devices.ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	value := devices.Value(`true`)
	s := &journeyDevices{stubDevices: &stubDevices{}, entity: devices.EntityWithState{
		Entity: devices.Entity{
			ID:        testHTTPEntityID,
			DeviceID:  testHTTPDeviceID,
			AdapterID: "simulator",
			Name:      "Power",
			Enabled:   true,
			TypeID:    devices.EntityTypePowerV1,
			Support:   devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		},
		Availability: devices.EntityAvailability{
			Status:     devices.EntityAvailabilityAvailable,
			Source:     "entity_report",
			Since:      now,
			EvidenceAt: now,
		},
	}, command: devices.CommandRecord{
		ID:                   "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID:             testHTTPEntityID,
		OperationName:        devices.OperationNameSet,
		Parameters:           devices.CommandParameters(`{"value":true}`),
		Status:               devices.CommandStatusSatisfied,
		RequestedAt:          now,
		DeadlineAt:           now.Add(10 * time.Second),
		CompletedAt:          &now,
		OutcomeObservationID: &observationID,
	}}
	var executions, inspections atomic.Int32
	s.getEntity = func(_ context.Context, id devices.EntityID) (devices.EntityWithState, error) {
		if id != testHTTPEntityID {
			return devices.EntityWithState{}, fmt.Errorf("wrong entity identity: %s", id)
		}
		inspections.Add(1)
		return s.entity, nil
	}
	s.executeCommand = func(_ context.Context, in devices.CommandInput) (devices.CommandResult, error) {
		if in.EntityID != testHTTPEntityID || in.OperationName != devices.OperationNameSet ||
			string(in.Parameters) != `{"value":true}` ||
			in.ID != "" ||
			in.CorrelationID != "" {
			return devices.CommandResult{}, fmt.Errorf("unexpected command: %+v", in)
		}
		executions.Add(1)
		return devices.NewCommandResult(s.command.ID, devices.OutcomeObserved, &observationID, &value)
	}
	server := newMCPServer(s, &stubAutomations{}, &stubNotifications{}, &stubDevices{}, &stubFunctions{}, nil)
	bridged, err := agent.MCPTools(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	if len(bridged) != 33 {
		t.Fatalf("agent tool count = %d", len(bridged))
	}
	tools := journeyToolsByName(ctx, t, bridged)
	invoke := func(name, args string) map[string]json.RawMessage {
		t.Helper()
		body, callErr := tools[name].InvokableRun(ctx, args)
		if callErr != nil {
			t.Fatal(callErr)
		}
		var fields map[string]json.RawMessage
		if decodeErr := json.Unmarshal([]byte(body), &fields); decodeErr != nil {
			t.Fatalf("%s: %s: %v", name, body, decodeErr)
		}
		if fields["failure_code"] != nil {
			t.Fatalf("%s failed: %s", name, body)
		}
		return fields
	}
	page := invoke("list_entities", `{}`)
	var summaries []map[string]json.RawMessage
	if err = json.Unmarshal(page["items"], &summaries); err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || string(summaries[0]["entity_id"]) != `"`+string(testHTTPEntityID)+`"` ||
		summaries[0]["support"] != nil ||
		summaries[0]["adapter_id"] != nil {
		t.Fatalf("summaries = %+v", summaries)
	}
	detail := invoke("get_entity", `{"entity_id":`+string(summaries[0]["entity_id"])+`}`)
	var support map[string]any
	if err = json.Unmarshal(detail["support"], &support); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		support,
		map[string]any{"state": map[string]any{}, "operations": map[string]any{"set": map[string]any{}}},
	) ||
		string(detail["state"]) != "null" {
		t.Fatalf("detail = %+v", detail)
	}
	receipt := invoke(
		"execute_entity_command",
		`{"entity_id":`+string(summaries[0]["entity_id"])+`,"operation":"set","parameters":{"value":true}}`,
	)
	if string(receipt["status"]) != `"satisfied"` || string(receipt["value"]) != "true" ||
		string(receipt["observation_id"]) != `"`+string(observationID)+`"` {
		t.Fatalf("receipt = %+v", receipt)
	}
	command := invoke("get_command", `{"command_id":`+string(receipt["command_id"])+`}`)
	if string(command["command_id"]) != string(receipt["command_id"]) || string(command["status"]) != `"satisfied"` ||
		string(command["outcome_observation_id"]) != string(receipt["observation_id"]) {
		t.Fatalf("command = %+v", command)
	}
	if executions.Load() != 1 || inspections.Load() != 1 || s.lists.Load() != 1 || s.commandReads.Load() != 1 {
		t.Fatalf(
			"service calls: execute=%d inspect=%d list=%d command=%d",
			executions.Load(),
			inspections.Load(),
			s.lists.Load(),
			s.commandReads.Load(),
		)
	}
}

func journeyToolsByName(ctx context.Context, t *testing.T, bridged []tool.BaseTool) map[string]tool.InvokableTool {
	t.Helper()
	tools := make(map[string]tool.InvokableTool, len(bridged))
	for _, candidate := range bridged {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			t.Fatalf("%s is not invokable", info.Name)
		}
		tools[info.Name] = invokable
	}
	return tools
}
