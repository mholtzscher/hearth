package musicassistant

import (
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter/mediaplayerv1"
)

const (
	// anchorDrift is how far the observed playhead may stray from the
	// extrapolated one before a new anchor is published. Music Assistant
	// refreshes elapsed time coarsely, so a smaller window would churn State.
	anchorDrift      = 2 * time.Second
	maxMediaSeconds  = 86400
	maxArtworkURL    = 2048
	anchorTimeLayout = "2006-01-02T15:04:05Z"
)

var (
	imageProxyPath = regexp.MustCompile(`^/imageproxy/([0-9a-f]{64})/?$`)
	imageSize      = regexp.MustCompile(`^[0-9]{1,4}$`)
	imageFormat    = regexp.MustCompile(`^[a-z]{1,8}$`)
)

// currentMedia is the part of a Music Assistant PlayerMedia the adapter reports.
type currentMedia struct {
	URI      string `json:"uri"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	ImageURL string `json:"image_url"`
	// Floats, so a fractional upstream value cannot fail the whole inventory.
	Duration *float64 `json:"duration"`
	Elapsed  *float64 `json:"elapsed_time"`
	// ElapsedUpdated is a Unix timestamp on Music Assistant's clock.
	ElapsedUpdated *float64 `json:"elapsed_time_last_updated"`
}

func (m currentMedia) duration() (int64, bool) {
	if m.Duration == nil {
		return 0, false
	}
	seconds := int64(math.Round(*m.Duration))
	if seconds < 1 || seconds > maxMediaSeconds {
		return 0, false
	}
	return seconds, true
}

// anchor is a published playhead: the position that was true at anchoredAt.
type anchor struct {
	mediaURI   string
	playback   string
	position   int64
	anchoredAt time.Time
}

// playheads holds the last anchor per player. A playing speaker keeps one
// anchor until its playhead jumps, so its State stays stable between polls.
type playheads map[string]anchor

// observe returns the anchor to publish for p at received, replacing the held
// anchor only when the media or playback changed or the playhead drifted.
func (h playheads) observe(p playerState, received time.Time) (anchor, bool) {
	if (p.State != playing && p.State != paused) || p.Media.URI == "" || p.Media.Elapsed == nil {
		delete(h, p.ID)
		return anchor{}, false
	}
	anchoredAt := received.UTC().Truncate(time.Second)
	observed := *p.Media.Elapsed
	if p.State == playing && p.Media.ElapsedUpdated != nil {
		updated := time.Unix(0, int64(*p.Media.ElapsedUpdated*float64(time.Second)))
		observed += anchoredAt.Sub(updated).Seconds()
	}
	observed = clampPosition(observed, p.Media)
	// A held anchor past a corrected, shorter duration is never kept.
	if previous, held := h[p.ID]; held && previous.mediaURI == p.Media.URI && previous.playback == p.State &&
		float64(previous.position) == clampPosition(float64(previous.position), p.Media) {
		expected := float64(previous.position)
		if previous.playback == playing {
			expected += anchoredAt.Sub(previous.anchoredAt).Seconds()
		}
		if math.Abs(observed-clampPosition(expected, p.Media)) <= anchorDrift.Seconds() {
			return previous, true
		}
	}
	next := anchor{
		mediaURI:   p.Media.URI,
		playback:   p.State,
		position:   int64(math.Round(observed)),
		anchoredAt: anchoredAt,
	}
	h[p.ID] = next
	return next, true
}

func clampPosition(seconds float64, media currentMedia) float64 {
	upper := float64(maxMediaSeconds)
	if duration, ok := media.duration(); ok {
		upper = float64(duration)
	}
	return math.Min(math.Max(seconds, 0), upper)
}

// artworkURL rebuilds a Music Assistant imageproxy URL on the adapter's own
// server origin. Music Assistant advertises imageproxy URLs on its configured
// base URL, which need not be reachable the way server_url is. Keeping only the
// opaque image ID, size, and format means no credential can reach State.
func artworkURL(origin, raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	match := imageProxyPath.FindStringSubmatch(u.Path)
	if match == nil {
		return "", false
	}
	query := url.Values{}
	if size := u.Query().Get("size"); imageSize.MatchString(size) {
		query.Set("size", size)
	}
	if format := u.Query().Get("fmt"); imageFormat.MatchString(format) {
		query.Set("fmt", format)
	}
	rebuilt := origin + "/imageproxy/" + match[1]
	if len(query) > 0 {
		rebuilt += "?" + query.Encode()
	}
	return rebuilt, len(rebuilt) <= maxArtworkURL
}

// mediaState adds the playhead and artwork, which need adapter memory and
// configuration, to a player's observed State.
func (a *Players) mediaState(p playerState, received time.Time) mediaplayerv1.State {
	state := p.entityState()
	if state.Playback == idle {
		delete(a.playheads, p.ID)
		return state
	}
	if head, ok := a.playheads.observe(p, received); ok {
		at := head.anchoredAt.Format(anchorTimeLayout)
		state.PositionSeconds, state.PositionAnchoredAt = new(head.position), &at
	}
	if artwork, ok := artworkURL(a.client.origin, strings.TrimSpace(p.Media.ImageURL)); ok {
		state.ArtworkUrl = &artwork
	}
	return state
}
