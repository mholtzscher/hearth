package musicassistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	contract "github.com/mholtzscher/hearth/entitytypes/mediaplayerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/mediaplayerv1"
)

const commandSetVolume = "players/cmd/volume_set"

// HandleCommand rechecks the target because Music Assistant can redirect
// playback controls for grouped players to a different speaker.
func (a *Players) HandleCommand(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
	if err := a.client.acquirePlayerIO(ctx); err != nil {
		return err
	}
	defer a.client.releasePlayerIO()
	playerID := a.routes[command.EntityID]
	if playerID == "" {
		return responder.RejectUnavailable("Player is not in the current account inventory")
	}
	player, err := a.client.readPlayer(ctx, command.ID, playerID)
	if err != nil || !player.available() {
		return responder.RejectUnavailable("Music Assistant player is unavailable")
	}
	if player.Kind != playerKind || player.grouped() || player.GroupMembers == nil {
		return responder.Reject("Player must be standalone")
	}
	support := player.entitySupport()
	if !supportsControl(support, command.OperationName) {
		return responder.Reject("Player does not support this control")
	}
	handler, err := a.playerCommandHandler(command.EntityID, player, support)
	if err != nil {
		return err
	}
	return handler(ctx, command, responder)
}

func (a *Players) playerCommandHandler(
	entityID string, player playerState, support mediaplayerv1.Support,
) (adapter.CommandHandler, error) {
	handlers := mediaplayerv1.Handlers{
		SetPlayback: func(ctx context.Context, cmd mediaplayerv1.SetPlaybackCommand, responder adapter.Responder) error {
			call := playbackControl(player.ID, cmd.Parameters)
			return a.executeControl(ctx, cmd.ID, entityID, player, responder, call)
		},
	}
	if support.Operations.SetVolume != nil {
		handlers.SetVolume = func(ctx context.Context, cmd mediaplayerv1.SetVolumeCommand, responder adapter.Responder) error {
			call := playerControl{
				method:  commandSetVolume,
				args:    volumeRequest{PlayerID: player.ID, Volume: int(cmd.Parameters.Percent)},
				matches: func(state mediaplayerv1.State) bool { return contract.SetVolumeSatisfied(cmd.Parameters, state) },
			}
			return a.executeControl(ctx, cmd.ID, entityID, player, responder, call)
		}
	}
	if support.Operations.SetMuted != nil {
		handlers.SetMuted = func(ctx context.Context, cmd mediaplayerv1.SetMutedCommand, responder adapter.Responder) error {
			args := struct {
				PlayerID string `json:"player_id"`
				Muted    bool   `json:"muted"`
			}{PlayerID: player.ID, Muted: cmd.Parameters.Muted}
			call := playerControl{
				method:  "players/cmd/volume_mute",
				args:    args,
				matches: func(state mediaplayerv1.State) bool { return contract.SetMutedSatisfied(cmd.Parameters, state) },
			}
			return a.executeControl(ctx, cmd.ID, entityID, player, responder, call)
		}
	}
	return mediaplayerv1.NewCommandHandler(entityID, support, handlers)
}

type playerControl struct {
	method  string
	args    any
	matches func(mediaplayerv1.State) bool
}

func (a *Players) executeControl(
	ctx context.Context, id, entityID string, player playerState, responder adapter.Responder, control playerControl,
) error {
	var reply json.RawMessage
	if err := a.client.call(ctx, id, control.method, control.args, &reply); err != nil {
		var status statusError
		if errors.As(err, &status) && status >= http.StatusBadRequest &&
			status < http.StatusInternalServerError && status != http.StatusRequestTimeout {
			return responder.Reject("Music Assistant refused the control")
		}
		return err
	}
	evidence, err := responder.Accept()
	if err != nil {
		return err
	}
	for {
		current, readErr := a.client.readPlayer(ctx, id, player.ID)
		if readErr != nil {
			return readErr
		}
		state := current.entityState()
		if !current.available() || current.Kind != playerKind || state.Grouping != standalone {
			return nil
		}
		if control.matches(state) {
			return a.observePlayer(ctx, entityID, current, time.Now().UTC(), evidence)
		}
		if !waitForRead(ctx) {
			return ctx.Err()
		}
	}
}

func supportsControl(support mediaplayerv1.Support, operation string) bool {
	switch operation {
	case contract.OperationSetPlayback:
		return support.Operations.SetPlayback != nil
	case contract.OperationSetVolume:
		return support.Operations.SetVolume != nil
	case contract.OperationSetMuted:
		return support.Operations.SetMuted != nil
	default:
		return false
	}
}

func playbackControl(playerID string, parameters mediaplayerv1.SetPlaybackParameters) playerControl {
	rpc := "players/cmd/stop"
	switch parameters.State {
	case playing:
		rpc = "players/cmd/play"
	case paused:
		rpc = "players/cmd/pause"
	}
	return playerControl{method: rpc, args: playerTarget{PlayerID: playerID},
		matches: func(state mediaplayerv1.State) bool { return contract.SetPlaybackSatisfied(parameters, state) }}
}
