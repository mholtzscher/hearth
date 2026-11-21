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
	playerID   = "bedroom-player"
	trackURI   = "library://track/1"
	playArgs   = `{"player_id":"bedroom-player","media_uri":"library://track/1","queue_mode":"replace","volume_percent":30}`
	testAuth   = "fixture-token"
	playerGet  = "players/get"
	mediaCheck = "music/verify_item_uri"
	volumeSet  = "players/cmd/volume_set"
	queuePlay  = "player_queues/play_media"
	queueGet   = "player_queues/get"
	idle30     = `{"player_id":"bedroom-player","type":"player","available":true,"group_members":[],"active_group":null,` +
		`"volume_level":30,"volume_muted":false,"playback_state":"idle","active_source":null}`
	syncFollower = `"active_group":null,"synced_to":"leader"`
	playing30    = `{"player_id":"bedroom-player","type":"player","available":true,"group_members":[],"active_group":null,` +
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

func readPlayer(body string) exchange {
	return exchange{command: playerGet, body: body, args: `{"player_id":"bedroom-player"}`}
}

func verifiedTrack() exchange {
	return exchange{command: mediaCheck, args: `{"uri":"library://track/1"}`, body: "true"}
}

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
	client, err := musicassistant.New("http://music.local:8095/", testAuth, transport)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func invoke(t *testing.T, c *musicassistant.Client, args string) adapter.FunctionResult {
	t.Helper()
	fn := discoveryFunction(t, c, "play_media")
	return fn.Handler(t.Context(), adapter.FunctionRequest{InvocationID: "example", Arguments: json.RawMessage(args)})
}

func TestPlayMediaConfirmsVolumeAndPlayback(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := newTestClient(t,
			verifiedTrack(),
			readPlayer(strings.Replace(idle30, `"volume_level":30`, `"volume_level":12`, 1)),
			setVolume(), readPlayer(idle30), startTrack(),
			readQueue(strings.Replace(correctQueue, trackURI, "library://track/2", 1)), readPlayer(playing30),
			readQueue(correctQueue), readPlayer(playing30),
		)
		requireDiscovery(t, discoveryFunction(t, c, "play_media"), strings.Replace(playArgs, `30`, `30.0`, 1),
			`{"status":"playing","player_id":"bedroom-player","media_uri":"library://track/1","volume_percent":30}`)
	})
}

func TestPlayMediaRefusesBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, reason string
	}{
		{"leader", strings.Replace(idle30, `"group_members":[]`, `"group_members":["other"]`, 1), "player_grouped_or_unsupported"},
		{"follower", strings.Replace(idle30, `"active_group":null`, `"active_group":"other"`, 1), "player_grouped_or_unsupported"},
		{"native sync follower at preset volume", strings.Replace(idle30, `"active_group":null`, syncFollower, 1),
			"player_grouped_or_unsupported"},
		{"native sync follower at another volume", strings.Replace(
			strings.Replace(idle30, `"active_group":null`, syncFollower, 1), `"volume_level":30`, `"volume_level":12`, 1),
			"player_grouped_or_unsupported"},
		{"unavailable", strings.Replace(idle30, `"available":true`, `"available":false`, 1), "player_unavailable"},
		{"muted", strings.Replace(idle30, `"volume_muted":false`, `"volume_muted":true`, 1), "player_muted"},
		{"incomplete", strings.Replace(idle30, `"group_members":[]`, `"group_members":null`, 1), "player_state_incomplete"},
		{"wrong_player", strings.Replace(idle30, playerID, "other", 1), "player_read_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := invoke(t, newTestClient(t, verifiedTrack(), readPlayer(tc.body)), playArgs)
			if got != (adapter.FunctionNotExecuted{ReasonCode: tc.reason}) {
				t.Fatalf("unexpected outcome: %+v", got)
			}
		})
	}
}

func TestPlayMediaDoesNotRetryMutations(t *testing.T) {
	t.Parallel()
	t.Run("transport loss", func(t *testing.T) {
		t.Parallel()
		call := startTrack()
		call.fail = true
		got := invoke(t, newTestClient(t, verifiedTrack(), readPlayer(idle30), call), playArgs)
		if got != (adapter.FunctionUnknown{ReasonCode: "mutation_unconfirmed"}) {
			t.Fatalf("unexpected outcome: %+v", got)
		}
	})
	t.Run("upstream refusal", func(t *testing.T) {
		t.Parallel()
		call := startTrack()
		call.status = http.StatusForbidden
		got := invoke(t, newTestClient(t, verifiedTrack(), readPlayer(idle30), call), playArgs)
		if got != (adapter.FunctionFailed{ReasonCode: "playback_rejected"}) {
			t.Fatalf("unexpected outcome: %+v", got)
		}
	})
	t.Run("joined native sync group after setting volume", func(t *testing.T) {
		t.Parallel()
		c := newTestClient(t,
			verifiedTrack(),
			readPlayer(strings.Replace(idle30, `"volume_level":30`, `"volume_level":12`, 1)),
			setVolume(), readPlayer(strings.Replace(idle30, `"active_group":null`, syncFollower, 1)),
		)
		if got := invoke(
			t,
			c,
			playArgs,
		); got != (adapter.FunctionUnknown{ReasonCode: "player_grouped_or_unsupported"}) {
			t.Fatalf("unexpected outcome: %+v", got)
		}
	})
}

func TestPlayMediaCannotConfirmPlaybackOnNativeSyncFollower(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, verifiedTrack(), readPlayer(idle30), startTrack(), readQueue(correctQueue),
		readPlayer(strings.Replace(playing30, `"active_group":null`, syncFollower, 1)))
	if got := invoke(t, c, playArgs); got != (adapter.FunctionUnknown{ReasonCode: "player_grouped_or_unsupported"}) {
		t.Fatalf("unexpected outcome: %+v", got)
	}
}

func TestPlayMediaRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	c := newTestClient(t)
	for _, input := range []string{
		`{}`, `{"player_id":"bedroom-player","media_uri":"library://track/1"}`,
		strings.Replace(playArgs, trackURI, "https://example.com/audio", 1),
		strings.Replace(playArgs, trackURI, "library://album/1", 1),
		strings.Replace(playArgs, `"replace"`, `"add"`, 1),
		strings.Replace(playArgs, `30`, `null`, 1),
		strings.Replace(playArgs, `30`, `101`, 1),
		strings.Replace(playArgs, `30`, `30.5`, 1),
		strings.Replace(playArgs, `30`, `30,"user":"admin"`, 1),
		strings.Replace(playArgs, `30`, `30,"volume_percent":50`, 1),
	} {
		if got := invoke(t, c, input); got != (adapter.FunctionNotExecuted{ReasonCode: "invalid_input"}) {
			t.Errorf("%s: unexpected outcome %+v", input, got)
		}
	}
}

func TestPlayMediaRefusesUnverifiedTrack(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, exchange{command: mediaCheck, args: `{"uri":"library://track/1"}`, body: "false"})
	if got := invoke(t, c, playArgs); got != (adapter.FunctionNotExecuted{ReasonCode: "media_unavailable"}) {
		t.Fatalf("unexpected outcome: %+v", got)
	}
}

func TestPlayMediaUsesRequestedTargetWithoutChangingVolume(t *testing.T) {
	t.Parallel()
	const (
		otherPlayer = "office-player"
		otherTrack  = "library://track/234"
	)
	player := func(body string) exchange {
		got := readPlayer(strings.ReplaceAll(body, playerID, otherPlayer))
		got.args = `{"player_id":"office-player"}`
		return got
	}
	c := newTestClient(
		t,
		exchange{command: mediaCheck, args: `{"uri":"library://track/234"}`, body: "true"},
		player(strings.Replace(idle30, `"volume_level":30`, `"volume_level":null`, 1)),
		exchange{
			command: queuePlay,
			args:    `{"queue_id":"office-player","media":"library://track/234","option":"play"}`,
			body:    "null",
		},
		exchange{command: queueGet, args: `{"queue_id":"office-player"}`,
			body: strings.ReplaceAll(strings.ReplaceAll(correctQueue, trackURI, otherTrack), playerID, otherPlayer)},
		player(strings.Replace(playing30, `"volume_level":30`, `"volume_level":null`, 1)),
	)
	requireDiscovery(t, discoveryFunction(t, c, "play_media"),
		`{"player_id":"office-player","media_uri":"library://track/234","queue_mode":"play"}`,
		`{"status":"playing","player_id":"office-player","media_uri":"library://track/234"}`)
}

func TestPlayMediaDoesNotTreatAcknowledgmentAsPlayback(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := newTestClient(
			t,
			verifiedTrack(),
			readPlayer(idle30),
			startTrack(),
			readQueue(correctQueue),
			readPlayer(idle30),
		)
		got := c.Functions()[0].Handler(t.Context(), adapter.FunctionRequest{
			Arguments: json.RawMessage(playArgs),
			Deadline:  time.Now().Add(100 * time.Millisecond),
		})
		if got != (adapter.FunctionUnknown{ReasonCode: "playback_unconfirmed"}) {
			t.Fatalf("unexpected outcome: %+v", got)
		}
	})
}
