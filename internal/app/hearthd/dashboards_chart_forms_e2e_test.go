//go:build e2e

package hearthd //nolint:testpackage // Drives production Core through public APIs and the SDK.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// dashboardChartFormsModel is a deterministic model: it discovers the
// Entities, then composes whichever view the user's message asks for.
type dashboardChartFormsModel struct{}

func (*dashboardChartFormsModel) Generate(
	_ context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	last := messages[len(messages)-1]
	if last.ToolName == "compose_view" {
		return schema.AssistantMessage("", nil), nil
	}
	if last.Role == schema.User {
		return dashboardJourneyCall("discover", "list_entities", "{}"), nil
	}
	tools := model.GetCommonOptions(nil, options...).Tools
	if last.ToolName == "list_entities" &&
		!slices.ContainsFunc(tools, func(info *schema.ToolInfo) bool { return info.Name == "compose_view" }) {
		return dashboardJourneyCall("discover-view", "tool_search", `{"query":"select:compose_view"}`), nil
	}
	var request string
	for _, message := range slices.Backward(messages) {
		if message.Role == schema.User {
			request = message.Content
			break
		}
	}
	ids := map[string]string{}
	for _, message := range slices.Backward(messages) {
		if message.ToolName != "list_entities" {
			continue
		}
		var inventory struct {
			Items []struct {
				ID   string `json:"entity_id"`
				Name string `json:"name"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(message.Content), &inventory); err != nil {
			return nil, err
		}
		for _, item := range inventory.Items {
			ids[item.Name] = item.ID
		}
		break
	}
	room, other, heater := ids["Forms room"], ids["Forms other room"], ids["Forms heater"]
	if room == "" || other == "" || heater == "" {
		return nil, errors.New("the chart form Entities were not discovered")
	}
	layout := fmt.Sprintf(
		`{"type":"Stack","children":[`+
			`{"type":"Ranking","title":"Warmest","rows":[{"entity_id":%[1]q},{"entity_id":%[2]q}]},`+
			`{"type":"WeekGrid","entity_id":%[1]q},`+
			`{"type":"PeriodTotals","entity_id":%[3]q,"period":"day","style":"columns"},`+
			`{"type":"TimeSeries","title":"Today","window":"24h","compare":"typical","series":[{"entity_id":%[1]q}]}]}`,
		room, other, heater,
	)
	if strings.Contains(request, "thresholds") {
		layout = fmt.Sprintf(
			`{"type":"TimeInRange","entity_id":%q,"window":"7d","thresholds":[20.5,22]}`, room,
		)
	}
	return dashboardJourneyCall(
		"compose", "compose_view", `{"mode":"create","title":"Chart forms","layout":`+layout+`}`,
	), nil
}

func (*dashboardChartFormsModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("fixture uses non-streaming generation")
}

func (m *dashboardChartFormsModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func journeyCards(t *testing.T, definition json.RawMessage) map[string]map[string]any {
	t.Helper()
	var view struct {
		Spec struct {
			Elements map[string]struct {
				Type  string         `json:"type"`
				Props map[string]any `json:"props"`
			} `json:"elements"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(definition, &view); err != nil {
		t.Fatal(err)
	}
	cards := map[string]map[string]any{}
	for _, element := range view.Spec.Elements {
		cards[element.Type] = element.Props
	}
	return cards
}

func TestDashboardChartFormsProductionJourney(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	config := Config{
		HouseholdTimezone: "UTC",
		NATSURL:           broker.ClientURL(),
		SQLitePath:        dbtest.MigratedPath(t),
		Agent:             requiredAgentConfig(t),
	}
	config.Agent.ChatModel = &dashboardChartFormsModel{}
	core := startFunctionCore(t, config)
	client := newNonPoolingHTTPClient(t)
	session, err := adapter.Connect(t.Context(), adapter.Config{
		AdapterID: "chart-forms", SoftwareName: "journey", SoftwareVersion: "1", NATSURL: broker.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	sensor := func(key, name string) adapter.EntityDescriptor {
		return adapter.EntityDescriptor{
			Key: key, ExternalID: key, Name: name, Type: "hearth.temperature/v1",
			Support: json.RawMessage(`{"state":{},"operations":{}}`),
		}
	}
	if _, err = session.Register(t.Context(), adapter.Registration{
		BindingKey: "forms",
		Device:     adapter.DeviceDescriptor{Name: "Forms device", Kind: "sensor"},
		Entities: []adapter.EntityDescriptor{
			sensor("room", "Forms room"),
			sensor("other", "Forms other room"),
			{
				Key: "heater", ExternalID: "heater", Name: "Forms heater", Type: "hearth.power/v1",
				Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	conversation := decodeNotification[struct {
		ID string `json:"id"`
	}](t, dashboardRequest(t, client, core, http.MethodPost, "/v1/agent/conversations", "", http.StatusOK))
	path := "/v1/agent/conversations/" + conversation.ID + "/messages"
	compose := func(text string) journeyArtifact {
		turn := decodeNotification[journeyTurn](t, dashboardRequest(
			t, client, core, http.MethodPost, path, `{"text":"`+text+`"}`, http.StatusOK,
		))
		if len(turn.ToolCalls) == 0 || turn.ToolCalls[len(turn.ToolCalls)-1] != "compose_view" ||
			len(turn.Artifacts) != 1 {
			t.Fatalf("agent turn for %q = %+v", text, turn)
		}
		saved := dashboardRequest(
			t, client, core, http.MethodPost, "/v1/dashboards",
			`{"definition":`+string(turn.Artifacts[0].Definition)+`}`, http.StatusCreated,
		)
		var dashboard journeyDashboard
		if decodeErr := json.Unmarshal(saved, &dashboard); decodeErr != nil || dashboard.Revision != 1 {
			t.Fatalf("saved dashboard: %s", saved)
		}
		return journeyArtifact{Definition: dashboard.Definition}
	}

	// A model that read 20.5 °C from discovery sends household numbers; the
	// presenter wired in run.go makes the saved thresholds canonical (mCel).
	inRange := journeyCards(t, compose("Show how long the room spent within thresholds 20.5 and 22").Definition)
	if want := []any{json.Number("20500"), json.Number("22000")}; !reflect.DeepEqual(
		journeyJSONValue(t, mustMarshal(t, inRange["TimeInRange"]["thresholds"])), want,
	) {
		t.Fatalf("saved thresholds = %v, want %v", inRange["TimeInRange"]["thresholds"], want)
	}

	cards := journeyCards(t, compose("Build the chart forms view").Definition)
	for _, name := range []string{"Ranking", "WeekGrid", "PeriodTotals", "TimeSeries"} {
		if cards[name] == nil {
			t.Fatalf("saved view lacks %s: %v", name, cards)
		}
	}
	if cards["TimeSeries"]["compare"] != "typical" {
		t.Fatalf("TimeSeries props = %v", cards["TimeSeries"])
	}
}

func mustMarshal(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
