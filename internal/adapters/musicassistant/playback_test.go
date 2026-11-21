package musicassistant_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/musicassistant"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	playerID  = "bedroom-player"
	trackURI  = "library://track/1"
	testAuth  = "fixture-token"
	playerGet = "players/get"
	volumeSet = "players/cmd/volume_set"
	queuePlay = "player_queues/play_media"
	queueGet  = "player_queues/get"
	idle30    = `{"player_id":"bedroom-player","type":"player","available":true,"group_members":[],"active_group":null,` +
		`"volume_level":30,"volume_muted":false,"playback_state":"idle","active_source":null}`
	playing30 = `{"player_id":"bedroom-player","type":"player","available":true,"group_members":[],"active_group":null,` +
		`"volume_level":30,"volume_muted":false,"playback_state":"playing","active_source":"bedroom-player"}`
	correctQueue = `{"queue_id":"bedroom-player","active":true,"state":"playing",` +
		`"current_item":{"media_item":{"uri":"library://track/1"}}}`
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type exchange struct {
	command string
	args    string
	body    string
	status  int
	fail    bool
}

func readPlayer(body string) exchange { return exchange{command: playerGet, body: body} }

func startTrack() exchange {
	return exchange{
		command: queuePlay, body: "null",
		args: `{"queue_id":"bedroom-player","media":"library://track/1","option":"replace"}`,
	}
}

func readQueue(body string) exchange { return exchange{command: queueGet, body: body} }

func setVolume() exchange {
	return exchange{command: volumeSet, args: `{"player_id":"bedroom-player","volume_level":30}`, body: "null"}
}

func newTestClient(t *testing.T, exchanges ...exchange) *musicassistant.Client {
	t.Helper()
	seen := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		var got struct {
			Command string          `json:"command"`
			Args    json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if seen >= len(exchanges) {
			t.Fatalf("unexpected request: %s", got.Command)
		}
		e := exchanges[seen]
		seen++
		if r.Method != http.MethodPost || r.URL.String() != "http://music.local:8095/api" ||
			r.Header.Get("Authorization") != "Bearer "+testAuth || r.GetBody != nil {
			t.Fatal("unexpected RPC transport or credentials")
		}
		if got.Command != e.command {
			t.Fatalf("got command %s, want %s", got.Command, e.command)
		}
		if e.args != "" && string(got.Args) != e.args {
			t.Fatalf("got args %s, want %s", got.Args, e.args)
		}
		if e.fail {
			return nil, errors.New("private fixture transport error")
		}
		status := e.status
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(e.body))}, nil
	})
	t.Cleanup(func() {
		if seen != len(exchanges) {
			t.Errorf("saw %d upstream calls, want %d", seen, len(exchanges))
		}
	})
	client, err := musicassistant.New(
		"http://music.local:8095/", testAuth,
		musicassistant.Preset{PlayerID: playerID, TrackURI: trackURI, VolumePercent: 30}, transport,
	)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func invoke(t *testing.T, c *musicassistant.Client, args string) adapter.FunctionResult {
	t.Helper()
	fn := c.Functions()[0]
	if fn.Descriptor.Key != "play_preset" {
		t.Fatalf("unexpected function %s", fn.Descriptor.Key)
	}
	return fn.Handler(t.Context(), adapter.FunctionRequest{InvocationID: "example", Arguments: json.RawMessage(args)})
}

func TestPresetConfirmsVolumeAndPlayback(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := newTestClient(t,
			readPlayer(strings.Replace(idle30, `"volume_level":30`, `"volume_level":12`, 1)),
			setVolume(), readPlayer(idle30), startTrack(),
			readQueue(strings.Replace(correctQueue, trackURI, "library://track/2", 1)), readPlayer(playing30),
			readQueue(correctQueue), readPlayer(playing30),
		)
		result, ok := invoke(t, c, "{}").(adapter.FunctionSucceeded)
		if !ok || string(result.Result) != `{"status":"playing","volume_percent":30}` {
			t.Fatalf("unexpected result: %+v", result)
		}
	})
}

func TestPresetRefusesBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, reason string
	}{
		{"leader", strings.Replace(idle30, `"group_members":[]`, `"group_members":["other"]`, 1), "player_grouped_or_unsupported"},
		{"follower", strings.Replace(idle30, `"active_group":null`, `"active_group":"other"`, 1), "player_grouped_or_unsupported"},
		{"unavailable", strings.Replace(idle30, `"available":true`, `"available":false`, 1), "player_unavailable"},
		{"muted", strings.Replace(idle30, `"volume_muted":false`, `"volume_muted":true`, 1), "player_muted"},
		{"incomplete", strings.Replace(idle30, `"group_members":[]`, `"group_members":null`, 1), "player_state_incomplete"},
		{"wrong_player", strings.Replace(idle30, playerID, "other", 1), "player_read_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := invoke(t, newTestClient(t, readPlayer(tc.body)), "{}")
			if got != (adapter.FunctionNotExecuted{ReasonCode: tc.reason}) {
				t.Fatalf("unexpected outcome: %+v", got)
			}
		})
	}
}

func TestPresetDoesNotRetryMutations(t *testing.T) {
	t.Parallel()
	t.Run("transport loss", func(t *testing.T) {
		t.Parallel()
		call := startTrack()
		call.fail = true
		got := invoke(t, newTestClient(t, readPlayer(idle30), call), "{}")
		if got != (adapter.FunctionUnknown{ReasonCode: "mutation_unconfirmed"}) {
			t.Fatalf("unexpected outcome: %+v", got)
		}
	})
	t.Run("upstream refusal", func(t *testing.T) {
		t.Parallel()
		call := startTrack()
		call.status = http.StatusForbidden
		got := invoke(t, newTestClient(t, readPlayer(idle30), call), "{}")
		if got != (adapter.FunctionFailed{ReasonCode: "playback_rejected"}) {
			t.Fatalf("unexpected outcome: %+v", got)
		}
	})
	t.Run("group changed after setting volume", func(t *testing.T) {
		t.Parallel()
		c := newTestClient(t,
			readPlayer(strings.Replace(idle30, `"volume_level":30`, `"volume_level":12`, 1)),
			setVolume(), readPlayer(strings.Replace(idle30, `"active_group":null`, `"active_group":"joined"`, 1)),
		)
		if got := invoke(t, c, "{}"); got != (adapter.FunctionUnknown{ReasonCode: "player_grouped_or_unsupported"}) {
			t.Fatalf("unexpected outcome: %+v", got)
		}
	})
}

func TestPresetRejectsOverrides(t *testing.T) {
	t.Parallel()
	c := newTestClient(t)
	if got := invoke(t, c, `{"player_id":"other"}`); got != (adapter.FunctionNotExecuted{ReasonCode: "invalid_input"}) {
		t.Fatalf("unexpected outcome: %+v", got)
	}
}

func TestPresetDoesNotTreatAcknowledgmentAsPlayback(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := newTestClient(t, readPlayer(idle30), startTrack(), readQueue(correctQueue), readPlayer(idle30))
		got := c.Functions()[0].Handler(t.Context(), adapter.FunctionRequest{
			Arguments: json.RawMessage("{}"),
			Deadline:  time.Now().Add(100 * time.Millisecond),
		})
		if got != (adapter.FunctionUnknown{ReasonCode: "playback_unconfirmed"}) {
			t.Fatalf("unexpected outcome: %+v", got)
		}
	})
}
