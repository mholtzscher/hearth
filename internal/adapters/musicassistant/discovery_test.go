package musicassistant_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/internal/adapters/musicassistant"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	listPlayersKey = "list_players"
	searchMediaKey = "search_media"
	playerListRPC  = "players/all"
	tracksRPC      = "music/tracks/library_items"
	titleQuery     = `{"query":"Ocean Sleep Sounds"}`
)

func discoveryFunction(t *testing.T, client *musicassistant.Client, key string) adapter.Function {
	t.Helper()
	for _, fn := range client.Functions() {
		if fn.Descriptor.Key == key {
			return fn
		}
	}
	t.Fatalf("%s was not registered", key)
	return adapter.Function{}
}

func invokeDiscovery(t *testing.T, fn adapter.Function, arguments string) adapter.FunctionResult {
	t.Helper()
	return fn.Handler(t.Context(), adapter.FunctionRequest{
		InvocationID: "example", Arguments: json.RawMessage(arguments),
	})
}

func requireDiscovery(t *testing.T, fn adapter.Function, arguments, want string) {
	t.Helper()
	schema, err := functionschema.Compile(fn.Descriptor.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(json.RawMessage(arguments)); err != nil {
		t.Fatalf("published input schema rejected the request: %v", err)
	}
	result := invokeDiscovery(t, fn, arguments)
	success, ok := result.(adapter.FunctionSucceeded)
	if !ok || string(success.Result) != want {
		t.Fatalf("got %+v (%s), want %s", result, success.Result, want)
	}
	schema, err = functionschema.Compile(fn.Descriptor.OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(success.Result); err != nil {
		t.Fatalf("published output schema rejected the result: %v", err)
	}
}

func TestListPlayersReturnsOnlyDiscoveryFields(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, exchange{
		command: playerListRPC, args: `{"return_unavailable":true}`,
		body: `[
			{"player_id":"bedroom","name":"Bedroom","type":"player","available":true,"group_members":[],"active_group":null},
			{"player_id":"office","name":"Office","type":"player","available":false,"active_group":"hidden-id"},
			{"player_id":"kitchen","name":"Kitchen","type":"player","available":true,"group_members":[],
			 "active_group":null,"synced_to":"hidden-leader"},
			{"player_id":"downstairs","name":"Downstairs","type":"group","available":true,"group_members":["hidden-member"]}
		]`,
	})
	requireDiscovery(t, discoveryFunction(t, c, listPlayersKey), "{}",
		`{"players":[{"player_id":"bedroom","name":"Bedroom","available":true,"grouped":false},`+
			`{"player_id":"office","name":"Office","available":false,"grouped":true},`+
			`{"player_id":"kitchen","name":"Kitchen","available":true,"grouped":true},`+
			`{"player_id":"downstairs","name":"Downstairs","available":true,"grouped":true}],"truncated":false}`)
}

func TestListPlayersBoundsResults(t *testing.T) {
	t.Parallel()
	var records []string
	for i := range 31 {
		records = append(records, fmt.Sprintf(
			`{"player_id":"p%d","name":"Room","type":"player","available":true}`, i))
	}
	c := newTestClient(t, exchange{command: playerListRPC, body: "[" + strings.Join(records, ",") + "]"})
	result := invokeDiscovery(t, discoveryFunction(t, c, listPlayersKey), "{}")
	success, ok := result.(adapter.FunctionSucceeded)
	var body struct {
		Players []struct {
			ID string `json:"player_id"`
		} `json:"players"`
		Truncated bool `json:"truncated"`
	}
	if !ok || json.Unmarshal(success.Result, &body) != nil || !body.Truncated || len(body.Players) != 30 ||
		body.Players[29].ID != "p29" {
		t.Fatalf("expected the first 30 players and a truncation flag, got %s", success.Result)
	}
}

// The endpoint names and media fields were verified against Music Assistant 2.10.12.
func TestSearchMediaProjectsLibraryResults(t *testing.T) {
	t.Parallel()
	c := newTestClient(
		t,
		exchange{
			command: tracksRPC,
			args:    `{"search":"Ocean Sleep Sounds","limit":10,"summary":true}`,
			body:    `[{"name":"Ocean Sleep Sounds","media_type":"track","uri":"library://track/1","provider":"library"}]`,
		},
		exchange{
			command: "music/albums/library_items",
			args:    `{"search":"Ocean Sleep Sounds","limit":9,"summary":true}`,
			body:    `[{"name":"Sleep Sounds","media_type":"album","uri":"library://album/2"}]`,
		},
		exchange{command: "music/artists/library_items", body: "[]"},
		exchange{command: "music/playlists/library_items", body: "[]"},
		exchange{command: "music/radios/library_items", body: "[]"},
		exchange{command: "music/audiobooks/library_items", body: "[]"},
		exchange{command: "music/podcasts/library_items", body: "[]"},
	)
	requireDiscovery(t, discoveryFunction(t, c, searchMediaKey), `{"query":"  Ocean Sleep Sounds "}`,
		`{"matches":[{"name":"Ocean Sleep Sounds","media_type":"track","uri":"library://track/1"},`+
			`{"name":"Sleep Sounds","media_type":"album","uri":"library://album/2"}]}`)
}

func TestSearchMediaStopsAtTenMatches(t *testing.T) {
	t.Parallel()
	var records []string
	for i := range 11 {
		records = append(records, fmt.Sprintf(
			`{"name":"Track %d","media_type":"track","uri":"library://track/%d"}`, i, i))
	}
	c := newTestClient(t, exchange{command: tracksRPC, body: "[" + strings.Join(records, ",") + "]"})
	result := invokeDiscovery(t, discoveryFunction(t, c, searchMediaKey), titleQuery)
	success, ok := result.(adapter.FunctionSucceeded)
	var body struct {
		Matches []struct {
			URI string `json:"uri"`
		} `json:"matches"`
	}
	if !ok || json.Unmarshal(success.Result, &body) != nil || len(body.Matches) != 10 ||
		body.Matches[9].URI != "library://track/9" {
		t.Fatalf("expected the first ten matches, got %s", success.Result)
	}
}

func TestDiscoveryRejectsOverrides(t *testing.T) {
	t.Parallel()
	c := newTestClient(t)
	for _, tc := range []struct{ key, input string }{
		{listPlayersKey, `{"return_disabled":true}`},
		{searchMediaKey, `{"query":"sleep","providers":["another-source"]}`},
		{searchMediaKey, `{"query":"sleep","query":"overwritten"}`},
		{searchMediaKey, `{"query":"  "}`},
		{searchMediaKey, `{"query":"` + strings.Repeat("é", 121) + `"}`},
	} {
		result := invokeDiscovery(t, discoveryFunction(t, c, tc.key), tc.input)
		if result != (adapter.FunctionNotExecuted{ReasonCode: "invalid_input"}) {
			t.Errorf("%s: unexpected result %+v", tc.key, result)
		}
	}
}

func TestDiscoveryDoesNotReturnInvalidOrPartialData(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		key       string
		input     string
		reason    string
		exchanges []exchange
	}{
		{"missing availability", listPlayersKey, "{}", "invalid_response", []exchange{
			{command: playerListRPC, body: `[{"player_id":"bedroom","name":"Bedroom","type":"player"}]`},
		}},
		{"nonlibrary URI", searchMediaKey, titleQuery, "invalid_response", []exchange{
			{command: tracksRPC, body: `[{"name":"External","media_type":"track","uri":"spotify://track/x"}]`},
		}},
		{"upstream failure", searchMediaKey, titleQuery, "discovery_read_failed", []exchange{
			{command: tracksRPC, body: `[{"name":"Ocean","media_type":"track","uri":"library://track/1"}]`},
			{command: "music/albums/library_items", status: http.StatusForbidden},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, tc.exchanges...)
			result := invokeDiscovery(t, discoveryFunction(t, c, tc.key), tc.input)
			if result != (adapter.FunctionFailed{ReasonCode: tc.reason}) {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}
