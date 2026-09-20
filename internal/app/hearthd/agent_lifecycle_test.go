package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

// TestRunFailsAtTheAgentStageWithoutLeakingTheSecretPath protects the required
// agent's startup contract: Core always constructs the agent, so a missing
// secret file fails Run at the agent stage, and the failure never repeats the
// configured path. It fails if a credential-less agent can start or if startup
// diagnostics echo the secret location.
func TestRunFailsAtTheAgentStageWithoutLeakingTheSecretPath(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := natstest.StartServer(t)
	secretPath := filepath.Join(t.TempDir(), "absent-secret", "agent-api-key")
	runErr := Run(ctx, Config{
		HouseholdTimezone: "UTC",
		HTTPAddr:          unusedLoopbackAddress(t),
		NATSURL:           server.ClientURL(),
		SQLitePath:        filepath.Join(t.TempDir(), "hearth.db"),
		Agent:             AgentConfig{APIKeyFile: secretPath},
	}, slog.New(slog.DiscardHandler))
	if runErr == nil {
		t.Fatal("Run started the required agent without a secret file")
	}
	if stage := ErrorStage(runErr); stage != "start_agent" {
		t.Fatalf("failed stage = %q, want start_agent (%v)", stage, runErr)
	}
	if strings.Contains(runErr.Error(), secretPath) || strings.Contains(runErr.Error(), "absent-secret") {
		t.Fatalf("startup failure repeated the secret path: %q", runErr)
	}
}

// TestAgentStageFailureFollowsTheSharedCancellationMapping protects the agent
// stage's cancellation mapping: a live startup reports the agent stage, while an
// explicitly canceled startup reports clean cancellation like every other stage.
func TestAgentStageFailureFollowsTheSharedCancellationMapping(t *testing.T) {
	t.Parallel()
	stageErr := failStage("start_agent", errors.New("agent.api_key_file could not be read"))
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

// TestHistoryPruneSchedulerPrunesAgentConversations protects the app wiring of
// agent retention through the shared history pruning worker: the required
// agent's own retention window deletes a whole expired conversation while a
// fresh conversation survives. It fails if the agent task is dropped from a
// pass or uses another module's window.
func TestHistoryPruneSchedulerPrunesAgentConversations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openOrderingDatabase(t)
	defer func() { _ = database.Close() }()
	now := time.Now().UTC()
	insertAgentConversationAt(t, database, "aconv_expired", now.Add(-40*24*time.Hour))
	insertAgentConversationAt(t, database, "aconv_fresh", now.Add(-time.Hour))

	agentService := newAgentServiceForTest(t, database, &stubAgentChatModel{}, 30*24*time.Hour)
	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	worker := startHistoryPruning(
		runContext, slog.New(slog.DiscardHandler),
		&stubHistoryPruner{name: devicesHistoryPruneModule},
		&stubHistoryPruner{name: automationsHistoryPruneModule},
		agentService,
	)
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		return countRetentionRows(ctx, database, "agent_conversations") == 1, nil
	})
	if stopErr := worker.Stop(context.Background()); stopErr != nil {
		t.Fatalf("stopping the history prune worker: %v", stopErr)
	}
	assertRetentionIDs(ctx, t, database, "agent_conversations", "id", []string{"aconv_fresh"})
}

// TestCoreShutdownDrainsAgentTurnsBeforeDependenciesAndDatabase protects the
// required agent's shutdown contract: admission closes before the join, a
// running turn is canceled and joined before shared dependencies are canceled,
// and SQLite closes only after that turn is gone. It fails if shutdown cancels
// dependencies or closes the database while a turn can still call a tool.
func TestCoreShutdownDrainsAgentTurnsBeforeDependenciesAndDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openOrderingDatabase(t)
	chatModel := newBlockingAgentChatModel()
	agentService := newAgentServiceForTest(t, database, chatModel, agent.MinimumConversationRetention)
	conversation, err := agentService.CreateConversation(ctx)
	if err != nil {
		t.Fatal(err)
	}

	turnErrors := make(chan error, 1)
	go func() {
		_, turnErr := agentService.SendMessage(ctx, conversation.ID, "hello")
		turnErrors <- turnErr
	}()
	select {
	case <-chatModel.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent turn never reached the model")
	}

	dependencyContext, cancelDependencies := context.WithCancel(context.Background())
	var cancelCount atomic.Int64
	shutdown := &coreShutdown{
		runContext:   ctx,
		logger:       slog.New(slog.DiscardHandler),
		database:     database,
		agentService: agentService,
		cancelDependencies: func() {
			cancelCount.Add(1)
			cancelDependencies()
		},
	}
	shutdownErrors := make(chan error, 1)
	go func() { shutdownErrors <- shutdown.run() }()

	// Admission closes promptly and refuses new turns while the admitted turn
	// still runs.
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return !agentService.AdmissionOpen(), nil
	})
	if _, sendErr := agentService.SendMessage(
		ctx, conversation.ID, "again",
	); !errors.Is(sendErr, agent.ErrAdmissionUnavailable) {
		t.Fatalf("turn during agent drain = %v, want ErrAdmissionUnavailable", sendErr)
	}
	// The admitted turn still needs its dependencies and the database.
	if got := cancelCount.Load(); got != 0 {
		t.Fatalf("shutdown canceled shared dependencies %d times before joining the turn", got)
	}
	if pingErr := database.Ping(); pingErr != nil {
		t.Fatalf("shutdown closed the database before joining the turn: %v", pingErr)
	}
	select {
	case shutdownErr := <-shutdownErrors:
		t.Fatalf("shutdown returned while the agent turn was running: %v", shutdownErr)
	default:
	}

	// Drain cancels the running turn; releasing it lets shutdown join and finish.
	select {
	case <-chatModel.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not cancel the running agent turn")
	}
	close(chatModel.release)
	select {
	case shutdownErr := <-shutdownErrors:
		if shutdownErr != nil {
			t.Fatalf("shutdown error = %v", shutdownErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join the agent turn")
	}
	select {
	case turnErr := <-turnErrors:
		if !errors.Is(turnErr, context.Canceled) {
			t.Fatalf("canceled turn error = %v, want context.Canceled", turnErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the canceled agent turn never returned")
	}
	if got := cancelCount.Load(); got != 1 {
		t.Fatalf("dependency cancellation count = %d, want 1", got)
	}
	if dependencyContext.Err() == nil {
		t.Fatal("shutdown left shared dependencies uncanceled")
	}
	if pingErr := database.Ping(); pingErr == nil {
		t.Fatal("shutdown left the database open")
	}
}

// insertAgentConversationAt seeds one agent conversation at a fixed instant in
// the module's own sortable timestamp layout.
func insertAgentConversationAt(t *testing.T, database *sql.DB, id string, createdAt time.Time) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO agent_conversations(id, created_at) VALUES (?, ?)`,
		id, createdAt.UTC().Format("2006-01-02T15:04:05.000000000Z"),
	); err != nil {
		t.Fatal(err)
	}
}
