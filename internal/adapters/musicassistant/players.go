package musicassistant

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/mediaplayerv1"
)

const (
	pollPlayersInterval = 5 * time.Second
	pollPlayersBudget   = 4 * time.Second
	mappingPageSize     = 200
	availabilityBatch   = 256
	playerEntityKey     = "playback"
	playerKind          = "player"
	playerReasonPrefix  = "adapter.hearth-adapter-music-assistant."
	idle                = "idle"
	paused              = "paused"
	unknown             = "unknown"
	standalone          = "standalone"
)

// Players registers and reports the account's speakers and handles their Commands.
type Players struct {
	client  *Client
	session *adapter.Session
	logger  *slog.Logger
	known   map[string]string
	routes  map[string]string
	last    map[string]string
}

// NewPlayers binds player inventory and Commands to an active Adapter Session.
func NewPlayers(client *Client, session *adapter.Session, logger *slog.Logger) *Players {
	return &Players{
		client: client, session: session, logger: logger,
		known: make(map[string]string), routes: make(map[string]string), last: make(map[string]string),
	}
}

// Run polls until cancellation. A failed upstream read makes the owner unhealthy;
// it does not turn retained playback into idle.
func (a *Players) Run(ctx context.Context) error {
	if err := a.loadMappings(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(pollPlayersInterval)
	defer ticker.Stop()
	for {
		err := a.poll(ctx)
		if errors.Is(err, adapter.ErrClosed) || errors.Is(err, adapter.ErrRuntimeFenced) {
			return err
		}
		if err != nil && ctx.Err() == nil {
			a.logger.WarnContext(ctx, "Music Assistant player publication failed",
				slog.String("event", "music_assistant.player_publication_failed"))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *Players) poll(ctx context.Context) error {
	// Waiting behind a control does not consume the observation budget.
	if err := a.client.acquirePlayerIO(ctx); err != nil {
		return err
	}
	defer a.client.releasePlayerIO()
	ctx, cancel := context.WithTimeout(ctx, pollPlayersBudget)
	defer cancel()

	players, err := a.client.playerInventory(ctx, "hearth-player-inventory")
	if err != nil || players == nil {
		return a.session.SetHealth(ctx, adapter.HealthReport{
			Status:           adapter.HealthUnhealthy,
			SourceObservedAt: time.Now().UTC(),
			ReasonCode:       playerReasonPrefix + "player_inventory_unavailable",
		})
	}
	received := time.Now().UTC()
	if err = a.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: received,
	}); err != nil {
		return err
	}
	return a.reconcile(ctx, players, received)
}

func (c *Client) playerInventory(ctx context.Context, id string) ([]playerState, error) {
	args := struct {
		ReturnUnavailable bool `json:"return_unavailable"`
	}{ReturnUnavailable: true}
	var players []playerState
	if err := c.call(ctx, id, "players/all", args, &players); err != nil {
		return nil, err
	}
	return players, nil
}

func (a *Players) reconcile(ctx context.Context, players []playerState, received time.Time) error {
	seen := make(map[string]bool, len(players))
	a.routes = make(map[string]string, len(players))
	reports := make([]adapter.EntityAvailabilityReport, 0, len(a.known))
	for _, p := range players {
		if p.Kind != playerKind || !boundedText(p.ID, maxIDRunes) || !boundedText(p.Name, maxIDRunes) {
			continue
		}
		key := playerBinding(p.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		id, err := a.registerPlayer(ctx, key, p)
		if err != nil {
			return err
		}
		a.routes[id] = p.ID
		report := adapter.EntityAvailabilityReport{EntityID: id, SourceObservedAt: received}
		if p.available() {
			if err = a.observePlayer(ctx, id, p, received, nil); err != nil {
				return err
			}
			report.Status = adapter.AvailabilityAvailable
		} else {
			report.Status, report.ReasonCode = adapter.AvailabilityUnavailable, playerReasonPrefix+"player_unavailable"
		}
		reports = append(reports, report)
	}
	for key, id := range a.known {
		if !seen[key] {
			reports = append(reports, adapter.EntityAvailabilityReport{
				EntityID: id, SourceObservedAt: received, Status: adapter.AvailabilityUnavailable,
				ReasonCode: playerReasonPrefix + "player_not_in_inventory",
			})
		}
	}
	for batch := range slices.Chunk(reports, availabilityBatch) {
		if err := a.session.ReportEntityAvailability(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

func (a *Players) loadMappings(ctx context.Context) error {
	request := adapter.OwnedMappingPageRequest{Limit: mappingPageSize}
	for {
		page, err := a.session.ListOwnedMappings(ctx, request)
		if err != nil {
			return err
		}
		for _, mapping := range page.Items {
			if mapping.EntityKey == playerEntityKey {
				a.known[mapping.BindingKey] = mapping.EntityID
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		request.Cursor = page.NextCursor
	}
}

func (a *Players) registerPlayer(ctx context.Context, key string, p playerState) (string, error) {
	support := p.entitySupport()
	descriptor, err := mediaplayerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: playerEntityKey, ExternalID: p.ID, Name: "Playback",
	}, support)
	if err != nil {
		return "", err
	}
	signature := p.Name + "\x00" + string(descriptor.Support)
	if id := a.known[key]; id != "" && a.last[key] == signature {
		return id, nil
	}
	binding, err := a.session.Register(ctx, adapter.Registration{
		BindingKey: key,
		Device:     adapter.DeviceDescriptor{ExternalID: &p.ID, Name: p.Name, Kind: "speaker"},
		Entities:   []adapter.EntityDescriptor{descriptor},
	})
	if err != nil {
		return "", err
	}
	if binding.BindingKey != key || len(binding.Entities) != 1 || binding.Entities[0].Key != playerEntityKey {
		return "", errors.New("player registration mapping mismatch")
	}
	id := binding.Entities[0].EntityID
	a.known[key], a.last[key] = id, signature
	return id, nil
}

func (a *Players) observePlayer(
	ctx context.Context, id string, p playerState, received time.Time, evidence adapter.CommandEvidence,
) error {
	observation, err := mediaplayerv1.NewObservation(mediaplayerv1.ObservationInput{
		EntityID: id, Support: p.entitySupport(), State: p.entityState(), AdapterReceivedAt: received,
	})
	if err != nil {
		return err
	}
	if evidence != nil {
		_, err = evidence.PublishObservation(ctx, observation)
	} else {
		_, err = a.session.PublishObservation(ctx, observation)
	}
	return err
}

func (p playerState) entityState() mediaplayerv1.State {
	state := mediaplayerv1.State{Playback: unknown, Grouping: unknown, Muted: p.Muted}
	switch p.State {
	case idle, playing, paused:
		state.Playback = p.State
	}
	switch {
	case p.grouped():
		state.Grouping = "grouped"
	case p.GroupMembers != nil:
		state.Grouping = standalone
	}
	if p.hasVolume() {
		state.VolumePercent = new(int64(*p.Volume))
	}
	if p.State != idle && boundedText(p.Media.Title, maxNameRunes) {
		state.Title = &p.Media.Title
	}
	return state
}

func (p playerState) entitySupport() mediaplayerv1.Support {
	operations := mediaplayerv1.OperationSupport{
		SetPlayback: &mediaplayerv1.SetPlaybackSupport{States: []string{idle, playing}},
	}
	if slices.Contains(p.Features, "pause") {
		operations.SetPlayback.States = append(operations.SetPlayback.States, paused)
	}
	if slices.Contains(p.Features, "volume_set") {
		operations.SetVolume = &mediaplayerv1.SetVolumeSupport{}
	}
	if slices.Contains(p.Features, "volume_mute") {
		operations.SetMuted = &mediaplayerv1.SetMutedSupport{}
	}
	return mediaplayerv1.Support{Operations: operations}
}

func playerBinding(id string) string {
	digest := sha256.Sum256([]byte(id))
	return "ma-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:]))
}
