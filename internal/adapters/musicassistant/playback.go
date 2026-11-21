package musicassistant

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	playing        = "playing"
	pollInterval   = 250 * time.Millisecond
	actionTimeout  = 15 * time.Second
	functionBudget = 18 * time.Second
	unconfirmed    = "playback_unconfirmed"
	maxVolume      = 100
	queueInsert    = "play"
	queueReplace   = "replace"
)

type playerState struct {
	playerGrouping

	ID        string   `json:"player_id"`
	Name      string   `json:"name"`
	Kind      string   `json:"type"`
	Available *bool    `json:"available"`
	Volume    *int     `json:"volume_level"`
	Muted     *bool    `json:"volume_muted"`
	State     string   `json:"playback_state"`
	Source    string   `json:"active_source"`
	Features  []string `json:"supported_features"`
	Media     struct {
		Title string `json:"title"`
	} `json:"current_media"`
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

type playbackTarget struct {
	PlayerID string
	MediaURI string
	Mode     string
	Volume   *int
}

func (c *Client) playbackFunction() adapter.Function {
	return adapter.Function{
		Descriptor: adapter.FunctionDescriptor{
			Key: "play_media",
			Description: "Play a Music Assistant library track on one ungrouped player. " +
				"Use play to insert and start it, or replace to replace the queue. Omit volume to leave it unchanged.",
			InputSchema: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["player_id","media_uri","queue_mode"],` +
					`"properties":{"player_id":{"type":"string","minLength":1,"maxLength":128},` +
					`"media_uri":{"type":"string","minLength":17,"maxLength":36},` +
					`"queue_mode":{"enum":["play","replace"]},"volume_percent":{"type":"integer","minimum":0,"maximum":100}}}`,
			),
			OutputSchema: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["status","player_id","media_uri"],` +
					`"properties":{"status":{"const":"playing"},"player_id":{"type":"string","minLength":1,"maxLength":128},` +
					`"media_uri":{"type":"string","minLength":17,"maxLength":36},` +
					`"volume_percent":{"type":"integer","minimum":0,"maximum":100}}}`,
			),
			TimeoutMS: int(functionBudget.Milliseconds()),
		},
		Handler: c.playMedia,
	}
}

func (c *Client) playMedia(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
	target, ok := parsePlaybackTarget(request.Arguments)
	if !ok {
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
	id := request.InvocationID
	if reason := c.verifyMedia(ctx, id, target.MediaURI); reason != "" {
		return adapter.FunctionNotExecuted{ReasonCode: reason}
	}
	if err := c.acquirePlayerIO(ctx); err != nil {
		return adapter.FunctionNotExecuted{ReasonCode: "player_busy"}
	}
	defer c.releasePlayerIO()
	player, err := c.readPlayer(ctx, id, target.PlayerID)
	if err != nil {
		return adapter.FunctionNotExecuted{ReasonCode: "player_read_failed"}
	}
	if reason := player.rejection(target.Volume != nil); reason != "" {
		return adapter.FunctionNotExecuted{ReasonCode: reason}
	}
	volumeChanged := target.Volume != nil && *player.Volume != *target.Volume
	if volumeChanged {
		var reply json.RawMessage
		volume := volumeRequest{PlayerID: target.PlayerID, Volume: *target.Volume}
		if err = c.call(ctx, id, commandSetVolume, volume, &reply); err != nil {
			return mutationFailure(err, "volume_rejected")
		}
		if reason := c.awaitVolume(ctx, id, target); reason != "" {
			return adapter.FunctionUnknown{ReasonCode: reason}
		}
	}
	return c.startTrack(ctx, id, target)
}

func (c *Client) verifyMedia(ctx context.Context, id, uri string) string {
	args := struct {
		URI string `json:"uri"`
	}{URI: uri}
	var accessible *bool
	if err := c.call(ctx, id, "music/verify_item_uri", args, &accessible); err != nil || accessible == nil {
		return "media_read_failed"
	}
	if !*accessible {
		return "media_unavailable"
	}
	return ""
}

func (c *Client) readPlayer(ctx context.Context, id, playerID string) (playerState, error) {
	var p playerState
	err := c.call(ctx, id, "players/get", playerTarget{PlayerID: playerID}, &p)
	if err == nil && p.ID != playerID {
		err = errors.New("upstream returned another player")
	}
	return p, err
}

func (p playerState) rejection(requireVolume bool) string {
	if !p.available() {
		return "player_unavailable"
	}
	if p.Kind != playerKind || p.grouped() {
		return "player_grouped_or_unsupported"
	}
	if p.GroupMembers == nil || p.Muted == nil || (requireVolume && !p.hasVolume()) {
		return "player_state_incomplete"
	}
	if *p.Muted {
		return "player_muted"
	}
	return ""
}

func (p playerState) available() bool {
	return p.Available != nil && *p.Available
}

func (p playerState) hasVolume() bool {
	return p.Volume != nil && *p.Volume >= 0 && *p.Volume <= maxVolume
}

func (c *Client) awaitVolume(ctx context.Context, id string, target playbackTarget) string {
	for {
		p, err := c.readPlayer(ctx, id, target.PlayerID)
		if err != nil {
			return "volume_unconfirmed"
		}
		if reason := p.rejection(true); reason != "" {
			return reason
		}
		if *p.Volume == *target.Volume {
			return ""
		}
		if !waitForRead(ctx) {
			return "volume_unconfirmed"
		}
	}
}

func (c *Client) startTrack(ctx context.Context, id string, target playbackTarget) adapter.FunctionResult {
	queueID := queueTarget{QueueID: target.PlayerID}
	var reply json.RawMessage
	if err := c.call(ctx, id, "player_queues/play_media", playRequest{
		QueueID: target.PlayerID, Media: target.MediaURI, Option: target.Mode,
	}, &reply); err != nil {
		return mutationFailure(err, "playback_rejected")
	}
	for {
		var queue queueState
		if err := c.call(ctx, id, "player_queues/get", queueID, &queue); err != nil || queue.ID != target.PlayerID {
			return adapter.FunctionUnknown{ReasonCode: unconfirmed}
		}
		player, err := c.readPlayer(ctx, id, target.PlayerID)
		if err != nil {
			return adapter.FunctionUnknown{ReasonCode: unconfirmed}
		}
		if reason := player.rejection(target.Volume != nil); reason != "" {
			return adapter.FunctionUnknown{ReasonCode: reason}
		}
		if target.confirmed(player, queue) {
			return target.playingResult(player)
		}
		if !waitForRead(ctx) {
			return adapter.FunctionUnknown{ReasonCode: unconfirmed}
		}
	}
}

func (target playbackTarget) confirmed(player playerState, queue queueState) bool {
	volumeMatches := target.Volume == nil || (player.hasVolume() && *player.Volume == *target.Volume)
	return volumeMatches && player.State == playing && player.Source == player.ID &&
		queue.Active && queue.State == playing && queue.Current.Media.URI == target.MediaURI
}

func (target playbackTarget) playingResult(player playerState) adapter.FunctionResult {
	var volume *int
	if player.hasVolume() {
		volume = player.Volume
	}
	result, err := json.Marshal(struct {
		Status   string `json:"status"`
		PlayerID string `json:"player_id"`
		MediaURI string `json:"media_uri"`
		Volume   *int   `json:"volume_percent,omitempty"`
	}{Status: playing, PlayerID: target.PlayerID, MediaURI: target.MediaURI, Volume: volume})
	if err != nil {
		return adapter.FunctionUnknown{ReasonCode: invalidResponse}
	}
	return adapter.FunctionSucceeded{Result: result}
}

func parsePlaybackTarget(arguments json.RawMessage) (playbackTarget, bool) {
	object, err := functionschema.NormalizeObject(arguments)
	if err != nil {
		return playbackTarget{}, false
	}
	var input struct {
		PlayerID string          `json:"player_id"`
		MediaURI string          `json:"media_uri"`
		Mode     string          `json:"queue_mode"`
		Volume   json.RawMessage `json:"volume_percent"`
	}
	if jsonv2.Unmarshal(object, &input, jsonv2.RejectUnknownMembers(true)) != nil ||
		!boundedText(input.PlayerID, maxIDRunes) ||
		strings.TrimSpace(input.PlayerID) != input.PlayerID ||
		!regexp.MustCompile(`^library://track/[0-9]{1,20}$`).MatchString(input.MediaURI) ||
		(input.Mode != queueInsert && input.Mode != queueReplace) {
		return playbackTarget{}, false
	}
	target := playbackTarget{PlayerID: input.PlayerID, MediaURI: input.MediaURI, Mode: input.Mode}
	if len(input.Volume) != 0 {
		number, ok := new(big.Rat).SetString(string(input.Volume))
		if !ok || !number.IsInt() || !number.Num().IsInt64() ||
			number.Sign() < 0 || number.Num().Int64() > maxVolume {
			return playbackTarget{}, false
		}
		value := int(number.Num().Int64())
		target.Volume = &value
	}
	return target, true
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
