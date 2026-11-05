package agent //nolint:testpackage // Exercises the in-memory committed-artifact ordering owner.

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func TestCommittedArtifactOrderingUsesRowIDs(t *testing.T) {
	t.Parallel()
	trace := &turnTrace{artifacts: []committedArtifact{
		{rowID: 20, artifact: dashboards.Artifact{ArtifactID: "second"}},
		{rowID: 5, artifact: dashboards.Artifact{ArtifactID: "first"}},
	}}
	ordered := trace.committedArtifacts()
	if len(ordered) != 2 || ordered[0].ArtifactID != "first" || ordered[1].ArtifactID != "second" {
		t.Fatalf("ordered=%+v", ordered)
	}
}
