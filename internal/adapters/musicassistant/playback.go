package musicassistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	playing        = "playing"
	pollInterval   = 250 * time.Millisecond
	actionTimeout  = 15 * time.Second
	functionBudget = 18 * time.Second
	unconfirmed    = "playback_unconfirmed"
)

type playerState struct {
	playerGrouping

	ID        string `json:"player_id"`
	Kind      string `json:"type"`
	Available bool   `json:"available"`
	Volume    *int   `json:"volume_level"`
	Muted     *bool  `json:"volume_muted"`
	State     string `json:"playback_state"`
	Source    string `json:"active_source"`
}

type queueState struct {
	ID      string `json:"queue_id"`
	Active  bool   `json:"active"`
	State   string `json:"state"`
	Current struct {
		Media struct {
			URI string `json:"uri"`
		} `json:"media_item"`
	} `json:"current_item"`
}

type playerTarget struct {
	PlayerID string `json:"player_id"`
}

type queueTarget struct {
	QueueID string `json:"queue_id"`
}

type volumeRequest struct {
	PlayerID string `json:"player_id"`
	Volume   int    `json:"volume_level"`
}

type playRequest struct {
	QueueID string `json:"queue_id"`
	Media   string `json:"media"`
	Option  string `json:"option"`
}

func (c *Client) playbackFunction() adapter.Function {
	return adapter.Function{
		Descriptor: adapter.FunctionDescriptor{
			Key:         "play_preset",
			Description: "Play the configured Music Assistant track on one ungrouped player at the preset volume.",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
			OutputSchema: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["status","volume_percent"],` +
					`"properties":{"status":{"const":"playing"},"volume_percent":{"type":"integer","minimum":1,"maximum":100}}}`,
			),
			TimeoutMS: int(functionBudget.Milliseconds()),
		},
		Handler: c.playPreset,
	}
}

func (c *Client) playPreset(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
	var args map[string]json.RawMessage
	if json.Unmarshal(request.Arguments, &args) != nil || args == nil || len(args) != 0 {
		return adapter.FunctionNotExecuted{ReasonCode: invalidInput}
	}
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	// The SDK also enforces its absolute deadline. Keep time to send the
	// outcome back after a slow Music Assistant startup.
	if !request.Deadline.IsZero() {
		deadlineCtx, stop := context.WithDeadline(ctx, request.Deadline)
		defer stop()
		ctx = deadlineCtx
	}
	player, err := c.readPlayer(ctx, request.InvocationID)
	if err != nil {
		return adapter.FunctionNotExecuted{ReasonCode: "player_read_failed"}
	}
	if reason := player.rejection(); reason != "" {
		return adapter.FunctionNotExecuted{ReasonCode: reason}
	}
	if *player.Volume != c.preset.VolumePercent {
		var reply json.RawMessage
		volume := volumeRequest{PlayerID: player.ID, Volume: c.preset.VolumePercent}
		if err = c.call(ctx, request.InvocationID, "players/cmd/volume_set", volume, &reply); err != nil {
			return mutationFailure(err, "volume_rejected")
		}
		if reason := c.awaitVolume(ctx, request.InvocationID); reason != "" {
			return adapter.FunctionUnknown{ReasonCode: reason}
		}
	}
	return c.startTrack(ctx, request.InvocationID, player.ID)
}

func (c *Client) readPlayer(ctx context.Context, id string) (playerState, error) {
	var p playerState
	err := c.call(ctx, id, "players/get", playerTarget{PlayerID: c.preset.PlayerID}, &p)
	if err == nil && p.ID != c.preset.PlayerID {
		err = errors.New("upstream returned another player")
	}
	return p, err
}

func (p playerState) rejection() string {
	if !p.Available {
		return "player_unavailable"
	}
	if p.Kind != "player" || p.grouped() {
		return "player_grouped_or_unsupported"
	}
	if p.GroupMembers == nil || p.Volume == nil || p.Muted == nil {
		return "player_state_incomplete"
	}
	if *p.Muted {
		return "player_muted"
	}
	return ""
}

func (c *Client) awaitVolume(ctx context.Context, id string) string {
	for {
		p, err := c.readPlayer(ctx, id)
		if err != nil {
			return "volume_unconfirmed"
		}
		if reason := p.rejection(); reason != "" {
			return reason
		}
		if *p.Volume == c.preset.VolumePercent {
			return ""
		}
		if !waitForRead(ctx) {
			return "volume_unconfirmed"
		}
	}
}

func (c *Client) startTrack(ctx context.Context, id, playerID string) adapter.FunctionResult {
	target := queueTarget{QueueID: playerID}
	var reply json.RawMessage
	if err := c.call(ctx, id, "player_queues/play_media", playRequest{
		QueueID: playerID, Media: c.preset.TrackURI, Option: "replace",
	}, &reply); err != nil {
		return mutationFailure(err, "playback_rejected")
	}
	for {
		var queue queueState
		if err := c.call(ctx, id, "player_queues/get", target, &queue); err != nil || queue.ID != playerID {
			return adapter.FunctionUnknown{ReasonCode: unconfirmed}
		}
		player, err := c.readPlayer(ctx, id)
		if err != nil {
			return adapter.FunctionUnknown{ReasonCode: unconfirmed}
		}
		if reason := player.rejection(); reason != "" {
			return adapter.FunctionUnknown{ReasonCode: reason}
		}
		if c.confirmed(player, queue) {
			return c.playingResult()
		}
		if !waitForRead(ctx) {
			return adapter.FunctionUnknown{ReasonCode: unconfirmed}
		}
	}
}

func (c *Client) confirmed(player playerState, queue queueState) bool {
	return *player.Volume == c.preset.VolumePercent && player.State == playing && player.Source == player.ID &&
		queue.Active && queue.State == playing && queue.Current.Media.URI == c.preset.TrackURI
}

func (c *Client) playingResult() adapter.FunctionResult {
	result, err := json.Marshal(struct {
		Status string `json:"status"`
		Volume int    `json:"volume_percent"`
	}{Status: playing, Volume: c.preset.VolumePercent})
	if err != nil {
		return adapter.FunctionUnknown{ReasonCode: invalidResponse}
	}
	return adapter.FunctionSucceeded{Result: result}
}

func waitForRead(ctx context.Context) bool {
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func mutationFailure(err error, refusal string) adapter.FunctionResult {
	var status statusError
	if errors.As(err, &status) && status >= http.StatusBadRequest && status < http.StatusInternalServerError &&
		status != http.StatusRequestTimeout {
		return adapter.FunctionFailed{ReasonCode: refusal}
	}
	return adapter.FunctionUnknown{ReasonCode: "mutation_unconfirmed"}
}
