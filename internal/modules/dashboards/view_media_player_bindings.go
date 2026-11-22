package dashboards

import "encoding/json"

// mediaPlayerPlaybackField is the always-present text State field a
// MediaPlayer validates its source through. The card reads the whole State.
const mediaPlayerPlaybackField = "/playback"

func mediaPlayerBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding `json:"source"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	source := props.Source
	source.Field, source.ValueKind = mediaPlayerPlaybackField, textValueKind
	return []binding{source}, nil
}
