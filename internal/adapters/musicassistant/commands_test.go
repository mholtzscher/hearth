package musicassistant //nolint:testpackage // Drives HandleCommand with a routed player behind the SDK seam.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const commandEntity = "ent_bedroom"

type rpcCall struct {
	command, args, body string
	status              int
}

// fakeResponder records how a Command ended. Dispatched operations never
// publish evidence, so Accept returns none.
type fakeResponder struct {
	accepted, rejected, unavailable bool
	message                         string
}

func (r *fakeResponder) Accept() (adapter.CommandEvidence, error) {
	r.accepted = true
	return nil, nil //nolint:nilnil // Dispatched controls take no evidence.
}

func (r *fakeResponder) Reject(message string) error {
	r.rejected, r.message = true, message
	return nil
}

func (r *fakeResponder) RejectUnavailable(message string) error {
	r.unavailable, r.message = true, message
	return nil
}

func routedPlayers(t *testing.T, calls ...rpcCall) *Players {
	t.Helper()
	seen := 0
	transport := roundTrip(func(r *http.Request) (*http.Response, error) {
		var got struct {
			Command string          `json:"command"`
			Args    json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if seen >= len(calls) {
			t.Fatalf("unexpected upstream call %s %s", got.Command, got.Args)
		}
		want := calls[seen]
		seen++
		if got.Command != want.command || (want.args != "" && string(got.Args) != want.args) {
			t.Fatalf("call %d = %s %s, want %s %s", seen, got.Command, got.Args, want.command, want.args)
		}
		status := want.status
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(want.body))}, nil
	})
	t.Cleanup(func() {
		if seen != len(calls) {
			t.Errorf("saw %d upstream calls, want %d", seen, len(calls))
		}
	})
	client, err := New(testOrigin+"/", "fixture-token", transport)
	if err != nil {
		t.Fatal(err)
	}
	players := NewPlayers(client, nil, nil)
	players.routes[commandEntity] = "bedroom-player"
	return players
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func transportCommand(operation, parameters string) adapter.Command {
	return adapter.Command{
		ID: "cmd_1", EntityID: commandEntity, OperationName: operation, Parameters: json.RawMessage(parameters),
		Deadline: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}
}

func readBedroom(body string) rpcCall {
	return rpcCall{command: "players/get", args: `{"player_id":"bedroom-player"}`, body: body}
}

func TestTransportCommandsDispatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, operation, parameters string
		upstream                    rpcCall
	}{
		{name: "next", operation: "next_track", parameters: `{}`,
			upstream: rpcCall{command: "players/cmd/next", args: `{"player_id":"bedroom-player"}`, body: "null"}},
		{name: "previous", operation: "previous_track", parameters: `{}`,
			upstream: rpcCall{command: "players/cmd/previous", args: `{"player_id":"bedroom-player"}`, body: "null"}},
		{name: "seek", operation: "seek", parameters: `{"position_seconds":150}`,
			upstream: rpcCall{command: "players/cmd/seek", args: `{"player_id":"bedroom-player","position":150}`, body: "null"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			players := routedPlayers(t, readBedroom(playingMedia), tc.upstream)
			responder := &fakeResponder{}
			err := players.HandleCommand(t.Context(), transportCommand(tc.operation, tc.parameters), responder)
			if err != nil || !responder.accepted || responder.rejected {
				t.Fatalf("err %v, accepted %v, rejected %v (%s); want dispatched", err, responder.accepted,
					responder.rejected, responder.message)
			}
		})
	}
}

func TestTransportCommandsRefuseBeforeWriting(t *testing.T) {
	t.Parallel()
	externalSource := mediaWith(t, `"active_source":"bedroom-player"`, `"active_source":"spotify"`)
	for _, tc := range []struct {
		name, operation, parameters, player string
		wantUnavailable                     bool
	}{
		{name: "skip on a source that cannot", operation: "next_track", parameters: `{}`, player: externalSource},
		{name: "seek without duration", operation: "seek", parameters: `{"position_seconds":10}`,
			player: mediaWith(t, `"duration":284`, `"duration":null`)},
		{name: "seek past the end", operation: "seek", parameters: `{"position_seconds":285}`, player: playingMedia},
		{name: "seek past a fractional end", operation: "seek", parameters: `{"position_seconds":284}`,
			player: mediaWith(t, `"duration":284`, `"duration":283.6`)},
		{name: "seek on a source that cannot", operation: "seek", parameters: `{"position_seconds":10}`, player: externalSource},
		{name: "grouped speaker", operation: "previous_track", parameters: `{}`,
			player: mediaWith(t, `"group_members":[]`, `"group_members":["kitchen"]`)},
		{name: "unavailable speaker", operation: "next_track", parameters: `{}`, wantUnavailable: true,
			player: mediaWith(t, `"available":true`, `"available":false`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			players := routedPlayers(t, readBedroom(tc.player))
			responder := &fakeResponder{}
			err := players.HandleCommand(t.Context(), transportCommand(tc.operation, tc.parameters), responder)
			if err != nil || responder.accepted || responder.unavailable != tc.wantUnavailable ||
				responder.rejected == tc.wantUnavailable {
				t.Fatalf("err %v, accepted %v, rejected %v, unavailable %v", err, responder.accepted,
					responder.rejected, responder.unavailable)
			}
		})
	}
}

func TestSkipNativelyWithoutTheQueue(t *testing.T) {
	t.Parallel()
	native := mediaWith(t, `"active_source":"bedroom-player"`,
		`"active_source":"line-in","supported_features":["next_previous"]`)
	players := routedPlayers(t, readBedroom(native),
		rpcCall{command: "players/cmd/next", args: `{"player_id":"bedroom-player"}`, body: "null"})
	responder := &fakeResponder{}
	if err := players.HandleCommand(
		t.Context(),
		transportCommand("next_track", `{}`),
		responder,
	); err != nil ||
		!responder.accepted {
		t.Fatalf("err %v, accepted %v; want dispatched", err, responder.accepted)
	}
}

func TestTransportCommandUpstreamFailures(t *testing.T) {
	t.Parallel()
	t.Run("client error rejects", func(t *testing.T) {
		t.Parallel()
		players := routedPlayers(t, readBedroom(playingMedia),
			rpcCall{command: "players/cmd/next", status: http.StatusBadRequest})
		responder := &fakeResponder{}
		if err := players.HandleCommand(
			t.Context(),
			transportCommand("next_track", `{}`),
			responder,
		); err != nil || !responder.rejected ||
			responder.accepted {
			t.Fatalf("err %v, rejected %v, accepted %v; want rejected", err, responder.rejected, responder.accepted)
		}
	})
	t.Run("server error fails without accepting", func(t *testing.T) {
		t.Parallel()
		players := routedPlayers(t, readBedroom(playingMedia),
			rpcCall{command: "players/cmd/seek", status: http.StatusInternalServerError})
		responder := &fakeResponder{}
		err := players.HandleCommand(t.Context(), transportCommand("seek", `{"position_seconds":5}`), responder)
		var status statusError
		if !errors.As(err, &status) || responder.accepted || responder.rejected {
			t.Fatalf(
				"err %v, accepted %v, rejected %v; want the upstream error",
				err,
				responder.accepted,
				responder.rejected,
			)
		}
	})
}

func TestEverySpeakerAdvertisesTransport(t *testing.T) {
	t.Parallel()
	support := decodePlayer(t, playingMedia).entitySupport().Operations
	if support.NextTrack == nil || support.PreviousTrack == nil || support.Seek == nil {
		t.Fatalf("transport support missing: %+v", support)
	}
}
