package musicassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	invalidInput    = "invalid_input"
	invalidResponse = "invalid_response"
	readFailed      = "discovery_read_failed"
	searchTimeout   = 4 * time.Second
	discoveryBudget = 5 * time.Second
	maxPlayers      = 30
	maxMatches      = 10
	maxQueryRunes   = 120
	maxNameRunes    = 200
	maxIDRunes      = 128
)

// Functions advertises account-scoped discovery and playback.
func (c *Client) Functions() []adapter.Function {
	return []adapter.Function{
		c.playbackFunction(),
		{
			Descriptor: adapter.FunctionDescriptor{
				Key:         "list_players",
				Description: "List enabled Music Assistant players visible to this account, including unavailable players.",
				InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
				OutputSchema: json.RawMessage(fmt.Sprintf(
					`{"type":"object","additionalProperties":false,"required":["players","truncated"],"properties":{`+
						`"players":{"type":"array","maxItems":%d,"items":{"type":"object","additionalProperties":false,`+
						`"required":["player_id","name","available","grouped"],"properties":{`+
						`"player_id":{"type":"string","minLength":1,"maxLength":%d},`+
						`"name":{"type":"string","minLength":1,"maxLength":%d},`+
						`"available":{"type":"boolean"},"grouped":{"type":"boolean"}}}},`+
						`"truncated":{"type":"boolean"}}}`, maxPlayers, maxIDRunes, maxNameRunes)),
				TimeoutMS: int(discoveryBudget.Milliseconds()),
			},
			Handler: c.listPlayers,
		},
		{
			Descriptor: adapter.FunctionDescriptor{
				Key:         "search_media",
				Description: "Find titles in the Music Assistant library visible to this account. Returns up to ten matches, tracks first.",
				InputSchema: json.RawMessage(fmt.Sprintf(
					`{"type":"object","additionalProperties":false,"required":["query"],`+
						`"properties":{"query":{"type":"string","minLength":1,"maxLength":%d}}}`, maxQueryRunes)),
				OutputSchema: json.RawMessage(fmt.Sprintf(
					`{"type":"object","additionalProperties":false,"required":["matches"],"properties":{`+
						`"matches":{"type":"array","maxItems":%d,"items":{"type":"object","additionalProperties":false,`+
						`"required":["name","media_type","uri"],"properties":{`+
						`"name":{"type":"string","minLength":1,"maxLength":%d},`+
						`"media_type":{"enum":["track","album","artist","playlist","radio","audiobook","podcast"]},`+
						`"uri":{"type":"string","minLength":1,"maxLength":%d}}}}}}`, maxMatches, maxNameRunes, maxIDRunes)),
				TimeoutMS: int(discoveryBudget.Milliseconds()),
			},
			Handler: c.searchMedia,
		},
	}
}

type playerSummary struct {
	ID        string `json:"player_id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Grouped   bool   `json:"grouped"`
}

type playerList struct {
	Players   []playerSummary `json:"players"`
	Truncated bool            `json:"truncated"`
}

func (c *Client) listPlayers(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
	var input map[string]json.RawMessage
	if json.Unmarshal(request.Arguments, &input) != nil || input == nil || len(input) != 0 {
		return adapter.FunctionNotExecuted{ReasonCode: invalidInput}
	}
	players, err := c.playerInventory(ctx, request.InvocationID)
	if err != nil {
		return adapter.FunctionFailed{ReasonCode: readFailed}
	}
	if players == nil {
		return adapter.FunctionFailed{ReasonCode: invalidResponse}
	}
	result := playerList{
		Players:   make([]playerSummary, 0, min(len(players), maxPlayers)),
		Truncated: len(players) > maxPlayers,
	}
	for _, p := range players[:min(len(players), maxPlayers)] {
		if p.Available == nil || p.Kind == "" || !boundedText(p.ID, maxIDRunes) || !boundedText(p.Name, maxNameRunes) {
			return adapter.FunctionFailed{ReasonCode: invalidResponse}
		}
		result.Players = append(result.Players, playerSummary{
			ID: p.ID, Name: p.Name, Available: *p.Available,
			Grouped: p.Kind == "group" || p.grouped(),
		})
	}
	return discoveryResult(result)
}

type mediaSearch struct {
	Search  string `json:"search"`
	Limit   int    `json:"limit"`
	Summary bool   `json:"summary"`
}

type mediaSummary struct {
	Name string `json:"name"`
	Type string `json:"media_type"`
	URI  string `json:"uri"`
}

type mediaMatches struct {
	Matches []mediaSummary `json:"matches"`
}

func (c *Client) searchMedia(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
	query, ok := searchQuery(request.Arguments)
	if !ok {
		return adapter.FunctionNotExecuted{ReasonCode: invalidInput}
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	// Library reads apply the current user's provider filter. Global search can
	// resolve shareable links or reuse results cached without the user's filter.
	kinds := [...]struct{ kind, collection string }{
		{"track", "tracks"}, {"album", "albums"}, {"artist", "artists"}, {"playlist", "playlists"},
		{"radio", "radios"}, {"audiobook", "audiobooks"}, {"podcast", "podcasts"},
	}
	result := mediaMatches{Matches: make([]mediaSummary, 0, maxMatches)}
	for _, kind := range kinds {
		args := mediaSearch{Search: query, Limit: maxMatches - len(result.Matches), Summary: true}
		var items []mediaSummary
		if err := c.call(
			ctx,
			request.InvocationID,
			"music/"+kind.collection+"/library_items",
			args,
			&items,
		); err != nil {
			return adapter.FunctionFailed{ReasonCode: readFailed}
		}
		if items == nil {
			return adapter.FunctionFailed{ReasonCode: invalidResponse}
		}
		for _, item := range items[:min(len(items), args.Limit)] {
			if !item.validLibraryItem(kind.kind) {
				return adapter.FunctionFailed{ReasonCode: invalidResponse}
			}
			result.Matches = append(result.Matches, item)
		}
		if len(result.Matches) == maxMatches {
			break
		}
	}
	return discoveryResult(result)
}

func (item mediaSummary) validLibraryItem(kind string) bool {
	itemID, libraryItem := strings.CutPrefix(item.URI, "library://"+kind+"/")
	return libraryItem && itemID != "" && item.Type == kind &&
		boundedText(item.URI, maxIDRunes) && boundedText(item.Name, maxNameRunes)
}

func searchQuery(arguments json.RawMessage) (string, bool) {
	object, err := functionschema.NormalizeObject(arguments)
	if err != nil {
		return "", false
	}
	var input map[string]string
	if json.Unmarshal(object, &input) != nil || len(input) != 1 || !boundedText(input["query"], maxQueryRunes) {
		return "", false
	}
	return strings.TrimSpace(input["query"]), true
}

func boundedText(text string, maxRunes int) bool {
	return strings.TrimSpace(text) != "" && utf8.RuneCountInString(text) <= maxRunes
}

func discoveryResult(result any) adapter.FunctionResult {
	encoded, err := json.Marshal(result)
	if err != nil {
		return adapter.FunctionFailed{ReasonCode: invalidResponse}
	}
	return adapter.FunctionSucceeded{Result: encoded}
}
