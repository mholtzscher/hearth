package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"testing"
)

// TestAgentStageFailureFollowsTheSharedCancellationMapping protects the agent
// stage's cancellation mapping: a live startup reports the agent stage, while an
// explicitly canceled startup reports clean cancellation like every other stage.
func TestAgentStageFailureFollowsTheSharedCancellationMapping(t *testing.T) {
	t.Parallel()
	stageErr := failStage("start_agent", errors.New("HEARTH_OPENAI_API_KEY is required"))
	if stage := ErrorStage(stageErr); stage != "start_agent" {
		t.Fatalf("ErrorStage = %q, want start_agent", stage)
	}
	if got := mapStartupCancellation(context.Background(), stageErr); !errors.Is(got, stageErr) {
		t.Fatalf("live agent stage error = %v, want the staged failure", got)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if got := mapStartupCancellation(canceled, stageErr); !errors.Is(got, context.Canceled) {
		t.Fatalf("canceled agent stage error = %v, want context.Canceled", got)
	}
}
