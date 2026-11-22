package musicassistant //nolint:testpackage // Exercises State mapping and the playhead anchor behind the SDK seam.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	testOrigin = "http://music.local:8095"
	proxyID    = "0f3c1d2e4b5a69788796a5b4c3d2e1f00f3c1d2e4b5a69788796a5b4c3d2e1f0"
	// Captured shape of a Music Assistant 2.10 queue item's PlayerMedia.
	playingMedia = `{"player_id":"bedroom-player","type":"player","available":true,"group_members":[],` +
		`"volume_level":42,"volume_muted":false,"playback_state":"playing","active_source":"bedroom-player",` +
		`"current_media":{"uri":"library://track/7","title":"Nocturne in F-Sharp Minor","artist":"Nils Frahm",` +
		`"album":"All Melody","image_url":"http://192.168.1.5:8095/imageproxy/` + proxyID + `?size=512&fmt=jpeg",` +
		`"duration":284,"elapsed_time":118,"elapsed_time_last_updated":1795378500.0}}`
)

func decodePlayer(t *testing.T, raw string) playerState {
	t.Helper()
	var p playerState
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func testPlayers(t *testing.T) *Players {
	t.Helper()
	client, err := New(testOrigin+"/", "fixture-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewPlayers(client, nil, nil)
}

func TestMediaStateReportsMetadataPlayheadAndArtwork(t *testing.T) {
	t.Parallel()
	p := decodePlayer(t, playingMedia)
	received := time.Unix(1795378502, 400_000_000).UTC()
	state := testPlayers(t).mediaState(p, received)
	want := map[string]string{
		"title": "Nocturne in F-Sharp Minor", "artist": "Nils Frahm", "album": "All Melody",
		"position_anchored_at": "2026-11-22T20:15:02Z",
		"artwork_url":          testOrigin + "/imageproxy/" + proxyID + "?fmt=jpeg&size=512",
	}
	got := map[string]string{
		"title": *state.Title, "artist": *state.Artist, "album": *state.Album,
		"position_anchored_at": *state.PositionAnchoredAt, "artwork_url": *state.ArtworkUrl,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
	// 118 s elapsed at 20:15:00, anchored at the whole second 20:15:02.
	if *state.DurationSeconds != 284 || *state.PositionSeconds != 120 {
		t.Errorf("duration, position = %d, %d; want 284, 120", *state.DurationSeconds, *state.PositionSeconds)
	}
}

func TestMediaStateOmitsWhatIsUnknown(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, from, to string
		check          func(*testing.T, playerState, *Players)
	}{
		{name: "idle omits every media field", from: `"playback_state":"playing"`, to: `"playback_state":"idle"`,
			check: func(t *testing.T, p playerState, players *Players) {
				state := players.mediaState(p, time.Now())
				if state.Title != nil || state.Artist != nil || state.DurationSeconds != nil ||
					state.PositionSeconds != nil || state.ArtworkUrl != nil {
					t.Errorf("idle State carried media fields: %+v", state)
				}
			}},
		{name: "radio without duration omits it", from: `"duration":284`, to: `"duration":null`,
			check: func(t *testing.T, p playerState, players *Players) {
				state := players.mediaState(p, time.Now())
				if state.DurationSeconds != nil || state.PositionSeconds == nil {
					t.Errorf("duration %v, position %v; want no duration with a position", state.DurationSeconds, state.PositionSeconds)
				}
			}},
		{name: "no elapsed time omits both position fields", from: `"elapsed_time":118`, to: `"elapsed_time":null`,
			check: func(t *testing.T, p playerState, players *Players) {
				state := players.mediaState(p, time.Now())
				if state.PositionSeconds != nil || state.PositionAnchoredAt != nil {
					t.Error("position or anchor published without elapsed time")
				}
			}},
		{name: "fractional values do not fail decoding", from: `"duration":284,"elapsed_time":118`,
			to: `"duration":283.6,"elapsed_time":118.4`,
			check: func(t *testing.T, p playerState, _ *Players) {
				if d, ok := p.Media.duration(); !ok || d != 284 {
					t.Errorf("duration = %d, %v; want 284", d, ok)
				}
			}},
		{name: "blank artist is omitted", from: `"artist":"Nils Frahm"`, to: `"artist":"  "`,
			check: func(t *testing.T, p playerState, players *Players) {
				if players.mediaState(p, time.Now()).Artist != nil {
					t.Error("blank artist published")
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := mediaWith(t, tc.from, tc.to)
			tc.check(t, decodePlayer(t, raw), testPlayers(t))
		})
	}
}

func TestPlayheadKeepsOneAnchorWhilePlayingSteadily(t *testing.T) {
	t.Parallel()
	heads := make(playheads)
	start := time.Unix(1795378500, 0).UTC()
	var first anchor
	for poll := range 10 {
		received := start.Add(time.Duration(poll) * 5 * time.Second)
		p := playerAt(t, "playing", "library://track/7", 118+float64(poll*5), received)
		head, ok := heads.observe(p, received)
		if !ok {
			t.Fatal("no anchor for a playing player")
		}
		if poll == 0 {
			first = head
		}
		if head != first {
			t.Fatalf("poll %d re-anchored: %+v, want %+v", poll, head, first)
		}
	}
}

func TestPlayheadReanchorsOnJumps(t *testing.T) {
	t.Parallel()
	start := time.Unix(1795378500, 0).UTC()
	later := start.Add(5 * time.Second)
	for _, tc := range []struct {
		name         string
		state, uri   string
		elapsed      float64
		wantPosition int64
	}{
		{name: "seek forward", state: "playing", uri: "library://track/7", elapsed: 153, wantPosition: 153},
		{name: "pause", state: "paused", uri: "library://track/7", elapsed: 123, wantPosition: 123},
		{name: "next track", state: "playing", uri: "library://track/8", elapsed: 0, wantPosition: 0},
		{name: "small drift is not a jump", state: "playing", uri: "library://track/7", elapsed: 124.5, wantPosition: 118},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			heads := make(playheads)
			heads.observe(playerAt(t, "playing", "library://track/7", 118, start), start)
			head, _ := heads.observe(playerAt(t, tc.state, tc.uri, tc.elapsed, later), later)
			if head.position != tc.wantPosition {
				t.Errorf("position = %d, want %d", head.position, tc.wantPosition)
			}
		})
	}
}

func TestPlayheadFreezesWhilePausedAndForgetsWhenIdle(t *testing.T) {
	t.Parallel()
	heads := make(playheads)
	start := time.Unix(1795378500, 0).UTC()
	paused := playerAt(t, "paused", "library://track/7", 60, start)
	first, _ := heads.observe(paused, start)
	again, _ := heads.observe(
		playerAt(t, "paused", "library://track/7", 60, start.Add(time.Minute)),
		start.Add(time.Minute),
	)
	if again != first {
		t.Errorf("paused playhead re-anchored: %+v, want %+v", again, first)
	}
	if _, ok := heads.observe(playerAt(t, "idle", "library://track/7", 60, start), start); ok {
		t.Error("idle player kept an anchor")
	}
	if len(heads) != 0 {
		t.Errorf("idle player left %d anchors", len(heads))
	}
}

func TestPlayheadReanchorsWhenDurationShrinksBelowIt(t *testing.T) {
	t.Parallel()
	heads := make(playheads)
	start := time.Unix(1795378500, 0).UTC()
	heads.observe(playerAt(t, "paused", "library://track/7", 200, start), start)
	corrected := playerAt(t, "paused", "library://track/7", 200, start)
	shorter := 100.0
	corrected.Media.Duration = &shorter
	head, _ := heads.observe(corrected, start.Add(5*time.Second))
	if head.position != 100 {
		t.Errorf("position = %d, want it clamped to the corrected 100 s duration", head.position)
	}
}

func TestPlayheadClampsToDuration(t *testing.T) {
	t.Parallel()
	start := time.Unix(1795378500, 0).UTC()
	// Music Assistant last updated elapsed time ten minutes ago, as after a clock step.
	p := playerAt(t, "playing", "library://track/7", 280, start.Add(-10*time.Minute))
	head, _ := make(playheads).observe(p, start)
	if head.position != 284 {
		t.Errorf("position = %d, want the 284 s duration", head.position)
	}
}

func TestArtworkURLKeepsOnlyTheImageProxyIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw, want string
	}{
		{name: "rebuilt on server_url", raw: "http://192.168.1.5:8095/imageproxy/" + proxyID + "?size=512&fmt=jpeg&x=y",
			want: testOrigin + "/imageproxy/" + proxyID + "?fmt=jpeg&size=512"},
		{name: "trailing slash", raw: "https://ma.example/imageproxy/" + proxyID + "/",
			want: testOrigin + "/imageproxy/" + proxyID},
		{name: "invalid size and format are dropped", raw: "http://h/imageproxy/" + proxyID + "?size=big&fmt=../x",
			want: testOrigin + "/imageproxy/" + proxyID},
		{name: "raw upstream URL", raw: "https://i.scdn.co/image/ab67616d0000b273"},
		{name: "short image ID", raw: "http://h/imageproxy/abc123"},
		{name: "uppercase image ID", raw: "http://h/imageproxy/" + "0F3C" + proxyID[4:]},
		{name: "extra path segment", raw: "http://h/imageproxy/foo/" + proxyID},
		{name: "credentials", raw: "http://user:secret@h/imageproxy/" + proxyID},
		{name: "file URL", raw: "file:///imageproxy/" + proxyID},
		{name: "empty", raw: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := artworkURL(testOrigin, tc.raw)
			if ok != (tc.want != "") || got != tc.want {
				t.Errorf("artworkURL(%q) = %q, %v; want %q", tc.raw, got, ok, tc.want)
			}
		})
	}
}

func playerAt(t *testing.T, state, uri string, elapsed float64, updated time.Time) playerState {
	t.Helper()
	p := decodePlayer(t, playingMedia)
	p.State, p.Media.URI = state, uri
	p.Media.Elapsed = &elapsed
	seconds := float64(updated.UnixNano()) / float64(time.Second)
	p.Media.ElapsedUpdated = &seconds
	return p
}

// mediaWith returns the playing fixture with one fragment replaced.
func mediaWith(t *testing.T, from, to string) string {
	t.Helper()
	if !strings.Contains(playingMedia, from) {
		t.Fatalf("fixture lacks %q", from)
	}
	return strings.Replace(playingMedia, from, to, 1)
}
