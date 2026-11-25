//go:build e2e

package hearthd //nolint:testpackage // The journey drives production Core through public APIs and the SDK.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This deterministic model proves the production tool/persistence journey, not
// hosted-model selection quality. The live browser check uses a real provider.
type dashboardJourneyModel struct{}

func (*dashboardJourneyModel) Generate(
	_ context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	last := messages[len(messages)-1]
	if last.ToolName == "compose_view" {
		return schema.AssistantMessage("", nil), nil
	}
	var edit bool
	for _, message := range slices.Backward(messages) {
		if message.Role == schema.User {
			edit = strings.Contains(message.Content, "Selected view draft")
			break
		}
	}
	if !edit && last.Role == schema.User {
		return dashboardJourneyCall("discover-temperature", "list_entities", "{}"), nil
	}
	tools := model.GetCommonOptions(nil, options...).Tools
	for _, info := range tools {
		if info.Name == "present_view" {
			return nil, errors.New("retired dashboard tool was advertised")
		}
	}
	if !slices.ContainsFunc(tools, func(info *schema.ToolInfo) bool { return info.Name == "compose_view" }) {
		return dashboardJourneyCall("discover-view", "tool_search", `{"query":"select:compose_view"}`), nil
	}
	if edit {
		return dashboardJourneyCall(
			"native-view-edit",
			"compose_view",
			`{"mode":"edit","changes":[{"op":"set_title","title":"Evening temperatures"},{"op":"set_chart_window","card_id":"node-5","window":"7d"}]}`,
		), nil
	}
	for _, message := range slices.Backward(messages) {
		if message.ToolName == "list_entities" {
			return composeDiscoveredTemperature(message.Content)
		}
	}
	return nil, errors.New("no inventory result before composition")
}

func composeDiscoveredTemperature(raw string) (*schema.Message, error) {
	var inventory struct {
		Items []struct {
			ID   string `json:"entity_id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &inventory); err != nil {
		return nil, err
	}
	for _, entity := range inventory.Items {
		if entity.Name != "Journey temperature" {
			continue
		}
		args := fmt.Sprintf(
			`{"mode":"create","title":"Household temperatures","layout":{"type":"Stack","children":[{"type":"Heading","text":"Indoor conditions"},{"type":"Grid","columns":2,"children":[{"type":"Metric","label":"Current","entity_id":%q},{"type":"TimeSeries","title":"Temperature history","series":[{"entity_id":%q}],"window":"24h"}]}]}}`,
			entity.ID,
			entity.ID,
		)
		return dashboardJourneyCall("native-view-create", "compose_view", args), nil
	}
	return nil, errors.New("the journey temperature was not discovered")
}

func dashboardJourneyCall(id, tool, arguments string) *schema.Message {
	return schema.AssistantMessage(
		"",
		[]schema.ToolCall{
			{
				ID:       id,
				Type:     "function",
				Function: schema.FunctionCall{Name: tool, Arguments: arguments},
			},
		},
	)
}

func (*dashboardJourneyModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("fixture uses non-streaming generation")
}

func (m *dashboardJourneyModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type journeyDashboard struct {
	ID         string          `json:"id"`
	Revision   int64           `json:"revision"`
	Definition json.RawMessage `json:"definition"`
}

type journeyArtifact struct {
	Kind       string          `json:"kind"`
	ArtifactID string          `json:"artifact_id"`
	Definition json.RawMessage `json:"definition"`
}

type journeyTurn struct {
	Reply     string            `json:"reply"`
	ToolCalls []string          `json:"tool_calls"`
	Artifacts []journeyArtifact `json:"artifacts"`
}

type journeyHistory struct {
	Messages []journeyHistoryMessage `json:"messages"`
}

type journeyHistoryMessage struct {
	Role       string            `json:"role"`
	ToolCallID string            `json:"tool_call_id"`
	ToolName   string            `json:"tool_name"`
	Artifacts  []journeyArtifact `json:"artifacts"`
}

var journeyArtifactID = regexp.MustCompile(
	`^view_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

func journeyJSONValue(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode JSON %s: %v", raw, err)
	}
	return value
}

func assertJourneyDefinition(
	t *testing.T,
	label string,
	definition, want json.RawMessage,
	title string,
) {
	t.Helper()
	var fields struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(definition, &fields); err != nil {
		t.Fatalf("%s definition: %v", label, err)
	}
	if fields.Title != title {
		t.Fatalf("%s title = %q, want %q: %s", label, fields.Title, title, definition)
	}
	if !reflect.DeepEqual(journeyJSONValue(t, definition), journeyJSONValue(t, want)) {
		t.Fatalf("%s definition = %s, want %s", label, definition, want)
	}
}

func assertJourneyArtifact(
	t *testing.T,
	label string,
	artifact journeyArtifact,
	want json.RawMessage,
	title string,
) {
	t.Helper()
	if artifact.Kind != "hearth.view" {
		t.Fatalf("%s kind = %q: %+v", label, artifact.Kind, artifact)
	}
	if !journeyArtifactID.MatchString(artifact.ArtifactID) {
		t.Fatalf("%s artifact id = %q", label, artifact.ArtifactID)
	}
	assertJourneyDefinition(t, label, artifact.Definition, want, title)
}

func assertJourneyTurnHistory(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	path string,
	turn journeyTurn,
	want json.RawMessage,
	title string,
	callID string,
) journeyArtifact {
	t.Helper()
	if len(turn.ToolCalls) == 0 || turn.ToolCalls[len(turn.ToolCalls)-1] != "compose_view" || len(turn.Artifacts) != 1 {
		t.Fatalf("agent turn for %q = %+v", title, turn)
	}
	artifact := turn.Artifacts[0]
	assertJourneyArtifact(t, "agent turn", artifact, want, title)
	history := decodeNotification[journeyHistory](
		t,
		dashboardRequest(t, client, core, http.MethodGet, path, "", http.StatusOK),
	)
	var message *journeyHistoryMessage
	for index := len(history.Messages) - 1; index >= 0; index-- {
		if history.Messages[index].Role == "tool" {
			message = &history.Messages[index]
			break
		}
	}
	if message == nil || message.ToolCallID != callID ||
		message.ToolName != "compose_view" || len(message.Artifacts) != 1 {
		t.Fatalf("missing native call artifact for %q: %+v", title, history)
	}
	assertJourneyArtifact(t, "agent history", message.Artifacts[0], want, title)
	if message.Artifacts[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("turn/history artifact ids differ: %+v / %+v", artifact, message.Artifacts[0])
	}
	return artifact
}

func dashboardRequest(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	method, path, body string,
	want int,
) []byte {
	t.Helper()
	status, raw, err := functionNetworkRequest(t.Context(), client, core.address, method, path, body)
	if err != nil || status != want {
		t.Fatalf("%s %s: status %d, want %d; %s; %v", method, path, status, want, raw, err)
	}
	return raw
}

func journeyTemperatureView(id, title, window string) json.RawMessage {
	return json.RawMessage(
		fmt.Sprintf(
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":%q,"spec":{"root":"node-1","elements":{"node-1":{"type":"Stack","props":{"gap":"medium"},"children":["node-2","node-3"]},"node-2":{"type":"Heading","props":{"text":"Indoor conditions"}},"node-3":{"type":"Grid","props":{"columns":2},"children":["node-4","node-5"]},"node-4":{"type":"Metric","props":{"label":"Current","source":{"kind":"entity-state","entity_id":%q,"entity_type":"hearth.temperature/v1"}}},"node-5":{"type":"TimeSeries","props":{"title":"Temperature history","series":[{"source":{"kind":"entity-series","entity_id":%q,"entity_type":"hearth.temperature/v1","window":%q}}]}}}}}`,
			title,
			id,
			id,
			window,
		),
	)
}

func assertDashboardToolsPrivate(t *testing.T, core *functionCoreRun) {
	t.Helper()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "dashboard-journey", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: "http://" + core.address + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) == 0 {
		t.Fatalf("production /mcp list: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "present_view" || tool.Name == "compose_view" {
			t.Fatalf("production /mcp exposed %s", tool.Name)
		}
	}
}

func TestDashboardProductionJourney(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	peer := &dashboardJourneyModel{}
	config := Config{
		HouseholdTimezone: "UTC",
		NATSURL:           broker.ClientURL(),
		SQLitePath:        dbtest.MigratedPath(t),
		Agent:             requiredAgentConfig(t),
	}
	config.Agent.ChatModel = peer
	core := startFunctionCore(t, config)
	client := newNonPoolingHTTPClient(t)
	session, err := adapter.Connect(
		t.Context(),
		adapter.Config{
			AdapterID:       "dashboard-sensor",
			SoftwareName:    "journey",
			SoftwareVersion: "1",
			NATSURL:         broker.ClientURL(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	binding, err := session.Register(t.Context(), adapter.Registration{
		BindingKey: "temperature",
		Device:     adapter.DeviceDescriptor{Name: "Journey sensor", Kind: "sensor"},
		Entities: []adapter.EntityDescriptor{
			{
				Key:        "temperature",
				ExternalID: "journey-temperature",
				Name:       "Journey temperature",
				Type:       "hearth.temperature/v1",
				Support:    json.RawMessage(`{"state":{},"operations":{}}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observationID, err := session.PublishObservation(
		t.Context(),
		adapter.Observation{
			EntityID:          entityID,
			Value:             json.RawMessage(`21500`),
			AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		raw := dashboardRequest(t, client, core, http.MethodGet, "/v1/entities/"+entityID, "", http.StatusOK)
		return strings.Contains(string(raw), string(observationID)), nil
	})
	definition := journeyTemperatureView(entityID, "Household temperatures", "24h")
	revised := journeyTemperatureView(entityID, "Evening temperatures", "7d")
	assertDashboardToolsPrivate(t, core)
	conversation := decodeNotification[struct {
		ID string `json:"id"`
	}](t, dashboardRequest(t, client, core, http.MethodPost, "/v1/agent/conversations", "", http.StatusOK))
	path := "/v1/agent/conversations/" + conversation.ID + "/messages"
	turn := decodeNotification[journeyTurn](
		t,
		dashboardRequest(
			t,
			client,
			core,
			http.MethodPost,
			path,
			`{"text":"Show my temperatures over the last 24 hours"}`,
			http.StatusOK,
		),
	)
	if want := []string{"list_entities", "tool_search", "compose_view"}; !slices.Equal(turn.ToolCalls, want) {
		t.Fatalf("production discovery and composition = %v, want %v", turn.ToolCalls, want)
	}
	artifact := assertJourneyTurnHistory(
		t,
		client,
		core,
		path,
		turn,
		definition,
		"Household temperatures",
		"native-view-create",
	)
	listed := decodeNotification[struct {
		Items      []struct{} `json:"items"`
		NextCursor string     `json:"next_cursor"`
	}](t, dashboardRequest(t, client, core, http.MethodGet, "/v1/dashboards", "", http.StatusOK))
	if listed.Items == nil || len(listed.Items) != 0 || listed.NextCursor != "" {
		t.Fatalf("presentation saved a dashboard: %+v", listed)
	}
	saved := decodeNotification[journeyDashboard](
		t,
		dashboardRequest(
			t,
			client,
			core,
			http.MethodPost,
			"/v1/dashboards",
			`{"definition":`+string(artifact.Definition)+`}`,
			http.StatusCreated,
		),
	)
	if saved.Revision != 1 {
		t.Fatalf("initial revision: %d", saved.Revision)
	}
	assertJourneyDefinition(t, "created dashboard", saved.Definition, definition, "Household temperatures")
	contextBody := fmt.Sprintf(
		`{"text":"Rename this view and switch the chart to 7d","view_context":{"definition":%s,"source_dashboard_id":%q,"source_revision":1}}`,
		saved.Definition,
		saved.ID,
	)
	revisedTurn := decodeNotification[journeyTurn](
		t,
		dashboardRequest(t, client, core, http.MethodPost, path, contextBody, http.StatusOK),
	)
	revisedArtifact := assertJourneyTurnHistory(
		t,
		client,
		core,
		path,
		revisedTurn,
		revised,
		"Evening temperatures",
		"native-view-edit",
	)
	unchanged := decodeNotification[journeyDashboard](
		t,
		dashboardRequest(t, client, core, http.MethodGet, "/v1/dashboards/"+saved.ID, "", http.StatusOK),
	)
	if unchanged.Revision != 1 {
		t.Fatal("agent revision overwrote saved dashboard")
	}
	assertJourneyDefinition(t, "unchanged dashboard", unchanged.Definition, definition, "Household temperatures")
	saved = decodeNotification[journeyDashboard](
		t,
		dashboardRequest(
			t,
			client,
			core,
			http.MethodPut,
			"/v1/dashboards/"+saved.ID,
			`{"expected_revision":1,"definition":`+string(revisedArtifact.Definition)+`}`,
			http.StatusOK,
		),
	)
	if saved.Revision != 2 {
		t.Fatalf("saved revision: %d", saved.Revision)
	}
	assertJourneyDefinition(t, "revised dashboard", saved.Definition, revised, "Evening temperatures")
	_ = dashboardRequest(
		t,
		client,
		core,
		http.MethodPut,
		"/v1/dashboards/"+saved.ID,
		`{"expected_revision":1,"definition":`+string(definition)+`}`,
		http.StatusConflict,
	)
	to := time.Now().UTC()
	seriesBody := fmt.Sprintf(
		`{"range":{"from":%q,"to":%q},"select_field":"","result":{"kind":"summary","metrics":{`+
			`"min_milli_celsius":{"operation":"min"},`+
			`"max_milli_celsius":{"operation":"max"},`+
			`"mean_milli_celsius":{"operation":"sample_mean"}}}}`,
		// Numeric summaries start on the one-minute rollup grid.
		to.Truncate(time.Minute).Add(-24*time.Hour).Format(time.RFC3339Nano),
		to.Format(time.RFC3339Nano),
	)
	series := dashboardRequest(
		t,
		client,
		core,
		http.MethodPost,
		"/v1/entities/"+entityID+"/state/history",
		seriesBody,
		http.StatusOK,
	)
	if !strings.Contains(string(series), `"mean_milli_celsius":21500`) {
		t.Fatalf("accepted Observation missing from series: %s", series)
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	core.stop()
	core.join(t)
	// Controlled retention fixture: the originating conversation is gone before restart.
	database, err := platformdb.Open(t.Context(), config.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(t.Context(), "DELETE FROM agent_conversations WHERE id = ?", conversation.ID)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	config.Agent.ChatModel = nil
	config.Agent.BaseURL = "http://127.0.0.1:1/v1"
	restarted := startFunctionCore(t, config)
	reopened := decodeNotification[journeyDashboard](
		t,
		dashboardRequest(t, client, restarted, http.MethodGet, "/v1/dashboards/"+saved.ID, "", http.StatusOK),
	)
	if reopened.Revision != 2 {
		t.Fatalf("reopened revision: %d", reopened.Revision)
	}
	assertJourneyDefinition(t, "reopened dashboard", reopened.Definition, revised, "Evening temperatures")
	_ = dashboardRequest(
		t,
		client,
		restarted,
		http.MethodDelete,
		"/v1/dashboards/"+saved.ID+"?expected_revision=2",
		"",
		http.StatusNoContent,
	)
}
