package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// conversationIDPrefix follows the house dev_/ent_/cmd_ ID style.
const conversationIDPrefix = "aconv_"

// rfc3339Nano matches the TEXT timestamp convention of the other tables.
const rfc3339Nano = time.RFC3339Nano

// defaultMaxIterations bounds the model-generation cycles in one turn.
const defaultMaxIterations = 10

// defaultSystemPrompt gives every conversation the same household-assistant
// behavior.
const defaultSystemPrompt = "You are a household assistant operating Hearth " +
	"through its tools. You can list and inspect entities, devices, adapters " +
	"and automations, read state, event, command and health histories, and " +
	"execute entity commands. " +
	"Entity and numeric aggregate-history reads return household-unit values by default; " +
	"read quantities (or history quantity) for units, labels, precision and bounds. " +
	"Use units:canonical when a reading is needed for a Command or Automation operand; " +
	"their input schemas remain canonical. Never copy a displayed number into a canonical write. " +
	"Raw observation audit and Automation definitions remain canonical. " +
	"Adapter Function arguments must use the values advertised by the function schema; " +
	"never pass a notification destination display name as an adapter-local destination key. " +
	"If a required value is not discoverable, ask the user instead of guessing. " +
	"Prefer a read tool before acting. For routine reads, call tools without announcing your plan. " +
	"When executing a command, state the " +
	"entity_id and operation first, then summarize the outcome plainly with IDs. " +
	"Discover canonical Entity and Device IDs with tools; never invent IDs. Use bound cards " +
	"for readings, not fabricated readings in text. Metric shows numeric fields, with optional trend " +
	"and range (only when the field declares bounds). TimeSeries shows numeric history. State shows " +
	"yes/no or text fields; supply true/false words for a binary sensor when its meaning is known. " +
	"ReadingList groups up to 12 mixed readings. Device follows a Device's readable Entities live. " +
	"StateTimeline shows when up to 12 yes/no or text fields changed over one window: style lanes compares " +
	"rows on one time axis, ledger leads with each current value and totals, feed lists the changes. " +
	"WeekPattern shows when one yes/no field was true, hour by hour, over the last 7 days. " +
	"ToggleTile is a tap-to-toggle tile bound to the root of a hearth.power/v1 Entity; use the Device name as its label. " +
	"For a metering plug, its optional metric has one explicit numeric source and window (24h by default), " +
	"shared by the displayed value and sparkline. Prefer discovered electrical power in watts, not cumulative energy; " +
	"the editor can change that source. Optional metric.detail adds a separately bound reading such as total energy. " +
	"All ToggleTile numeric sources must belong to the power Entity's Device; do not guess a source by name or list order. " +
	"ToggleList groups 1–12 distinct hearth.power/v1 Entities with Device-name row labels; all_off:true offers native Turn all off. " +
	"Use a Grid of ToggleTiles for quick switches, with up to 4 columns. Ordinary toggle tiles have no sparkline. " +
	"For a Device of kind air-purifier, use AirPurifier. Bind power and a PM2.5 numeric sensor in µg/m³; bind mode, " +
	"fan_level, display, child_lock, and filter_life when the Device has a matching Entity. Identify Display and Child " +
	"lock by their Entity names; never guess. Omit a role you cannot identify. Use layout card on its own or in a " +
	"dashboard, and layout tile in a Grid of quick switches. Every bound Entity must belong to the power Entity's Device. " +
	"For a weather station, prefer the weather cards over separate Metrics. They bind named slots, each read from an Entity's root: " +
	"OutsideNow needs an outdoor hearth.temperature/v1 and optionally humidity, wind (speed), wind_direction, rain_rate, and rain_today; " +
	"it words conditions from the rain rate and wind speed but shows no feels-like or dew point. " +
	"Rain needs a rain rate; storm and totals (day, week, month, year) " +
	"bind the station's own counters, never sums of the rate. Wind needs speed, optionally gust, direction, and max_gust. " +
	"Pressure binds a hearth.pressure/v1 Entity and states its three-hour tendency. Slots taking hearth.numericsensor/v1 require " +
	"its discovered unit: mm/h for a rain rate, mm for rain totals, deg for wind direction. Every rain counter is in mm, so " +
	"also match each storm, day, week, month, or year slot to the sensor named for that period, such as Daily Rain for day; " +
	"leave an optional slot out rather than guess. " +
	"Forecast binds one hourly or daily forecast Entity; hourly periods is 6–48 (default 24) and daily 3–8 (default 8). It shows modeled forecasts, not station readings. " +
	"Controls derive from live Entity support and send Commands only on an explicit browser gesture. " +
	"Presenting, rendering, refreshing, editing, or saving a view never executes Commands. Presenting a view creates a draft; " +
	"only an explicit user save saves a dashboard. " +
	"A presented visual is the answer. The user sees its cards and notes live: do not repeat their readings, " +
	"values, entity lists, labels, coverage, or caveats in text. After successful presentation, finish with " +
	"an empty text response unless the user needs information or a decision not already conveyed by the visual. " +
	"In that case, use at most one short sentence. Do not recap your work or manufacture a follow-up. " +
	"To show a saved Automation as a card, use present_automation with mode reference and its discovered automation_id. " +
	"Automation cards are separate from dashboard view cards. " +
	"For Automation creation or replacement, use present_automation to propose a draft; " +
	"only an explicit browser Save creates or replaces it. Presenting does not run an Automation."

// Turn event types streamed while a turn runs; clients render tool activity
// incrementally and reconcile with history on turn.finished.
const (
	// EventTurnStarted opens one streamed turn.
	EventTurnStarted = "turn.started"
	// EventToolPreparing announces a tool call the model has begun writing,
	// before its arguments are complete. tool.started follows only if the
	// model's response completes and the call runs.
	EventToolPreparing = "tool.preparing"
	// EventToolStarted announces one tool execution with its arguments.
	EventToolStarted = "tool.started"
	// EventToolFinished delivers one tool result (truncated for the stream;
	// the full row stays in history).
	EventToolFinished = "tool.finished"
	// EventViewPresented delivers a committed view artifact.
	EventViewPresented = "view.presented"
	// EventAutomationPresented delivers a committed Automation artifact.
	EventAutomationPresented = "automation.presented"
	// EventTurnFinished closes the turn with the reply text.
	EventTurnFinished = "turn.finished"
	// EventTurnFailed closes the turn with a generic failure; the cause
	// stays server-side like every other boundary.
	EventTurnFailed = "turn.failed"
)

// maxEventResult bounds tool result bytes per stream event.
const maxEventResult = 4000

// maxMessageRunes bounds one user message. The blocking transport repeats the
// same 4000 in its Huma maxLength tag, which cannot reference a constant.
const maxMessageRunes = 4000

// historyRowBudget bounds how many persisted rows one turn rebuilds from a
// conversation. Each model response occupies one row, including a parallel
// tool-call batch; each completed tool result occupies its own row.
const historyRowBudget = 200

// historyByteBudget bounds encoded prepared messages, including the current
// user message. Bytes, not tokens, keep the bound provider-independent. Only
// whole recent turns inside it are kept, so an active conversation cannot grow
// until the provider rejects every later turn.
const historyByteBudget = 256 * 1024

// TurnEvent is one streamed turn update.
type TurnEvent struct {
	Type      string    `json:"type"`
	CallID    string    `json:"call_id,omitempty"`
	Artifact  *Artifact `json:"artifact,omitempty"`
	Name      string    `json:"name,omitempty"`
	Arguments string    `json:"arguments,omitempty"`
	Result    string    `json:"result,omitempty"`
	Reply     string    `json:"reply,omitempty"`
	ToolCalls []string  `json:"tool_calls,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// truncateEventResult keeps stream frames small; history keeps full rows.
func truncateEventResult(result string) string {
	if len(result) <= maxEventResult {
		return result
	}
	return result[:maxEventResult] + "…[truncated]"
}

// ErrConversationNotFound is returned when a turn or read names an unknown
// conversation.
var ErrConversationNotFound = errors.New("agent conversation not found")

// messageTextError marks invalid message input or a model-input budget failure.
// Its fixed client-facing detail does not echo rejected values. Budget failures
// can occur after the user message has been persisted.
type messageTextError struct{ detail string }

func (invalid messageTextError) Error() string { return invalid.detail }

// validateMessageText rejects the user text neither transport accepts as a
// message. The blocking route's Huma schema declares the same empty and length
// bounds; this check backs it up and is the only bound the schema-less SSE
// route has.
func validateMessageText(text string) error {
	trimmed := strings.TrimSpace(text)
	switch {
	case trimmed == "":
		return messageTextError{detail: "agent message text is required"}
	case len([]rune(trimmed)) > maxMessageRunes:
		return messageTextError{
			detail: fmt.Sprintf("agent message text must be at most %d characters", maxMessageRunes),
		}
	}
	return nil
}

// Config assembles the agent service. The model is shared, while each turn
// constructs an ADK runner. ChatModel replaces the constructed provider model for
// tests and embedding; when it is set, Model credentials are not required. A
// nil Logger selects [slog.Default].
type Config struct {
	DB            *sql.DB
	Tools         []tool.BaseTool
	Model         ModelConfig
	ChatModel     model.ToolCallingChatModel
	MaxIterations int
	// Retention bounds whole-conversation history. It must be at least
	// MinimumConversationRetention for PruneHistory to run; zero is
	// unconfigured and fails safely at prune time, never at construction.
	Retention time.Duration
	Logger    *slog.Logger
}

// ErrAdmissionUnavailable reports that agent turn admission is closed, so
// StopAdmission or Drain has run and new turns are rejected.
var ErrAdmissionUnavailable = errors.New("agent admission is unavailable")

// Service is the household agent: Eino ADK over the Core MCP catalog with
// durable SQLite conversation history.
type Service struct {
	database      *sql.DB
	queries       *dbsqlc.Queries
	chatModel     model.ToolCallingChatModel
	provider      Provider
	tools         []tool.BaseTool
	skills        embeddedSkills
	maxIterations int
	retention     time.Duration
	logger        *slog.Logger

	// admission gates new turns and tracks admitted ones for Drain.
	admission *lifecycle.AdmissionGroup
	// turnsMu guards turns, whose cancel functions let Drain cancel a running
	// turn before joining it through admission.
	turnsMu  sync.Mutex
	turns    map[uint64]context.CancelFunc
	nextTurn uint64

	// gatesMu guards gates, which serialize turns per conversation so two
	// clients cannot build the same history snapshot or interleave rows.
	gatesMu sync.Mutex
	gates   map[string]*conversationGate
}

// conversationGate is one conversation's turn semaphore: token admits one turn,
// and refs counts the turns holding or waiting on it so an idle conversation
// drops its entry instead of accumulating state.
type conversationGate struct {
	token chan struct{}
	refs  int
}

// NewService builds the agent service and its chat model.
func NewService(_ context.Context, config Config) (*Service, error) {
	if err := config.Model.ValidateSettings(); err != nil {
		return nil, err
	}
	switch {
	case config.DB == nil:
		return nil, errors.New("agent database is required")
	case len(config.Tools) == 0:
		return nil, errors.New("agent tools are required")
	case config.MaxIterations < 0:
		return nil, errors.New("agent max_iterations must not be negative")
	case config.ChatModel == nil && strings.TrimSpace(config.Model.APIKey) == "":
		return nil, errors.New("agent model API key is required")
	case config.ChatModel == nil && strings.TrimSpace(config.Model.Model) == "":
		return nil, errors.New("agent model name is required")
	}
	skills, skillErr := newEmbeddedSkills()
	if skillErr != nil {
		return nil, fmt.Errorf("agent skills: %w", skillErr)
	}
	chatModel := config.ChatModel
	if chatModel == nil {
		if config.Model.Provider.Effective() == ProviderOpenCodeGo {
			chatModel = newGoChatModel(config.Model)
		} else {
			chatModel = newResponsesModel(config.Model)
		}
	}
	maxIterations := config.MaxIterations
	if maxIterations == 0 {
		maxIterations = defaultMaxIterations
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		database:      config.DB,
		queries:       dbsqlc.New(config.DB),
		chatModel:     chatModel,
		provider:      config.Model.Provider.Effective(),
		tools:         config.Tools,
		skills:        skills,
		maxIterations: maxIterations,
		retention:     config.Retention,
		logger:        logger,
		admission:     lifecycle.NewAdmissionGroup(),
		turns:         make(map[uint64]context.CancelFunc),
		gates:         make(map[string]*conversationGate),
	}, nil
}

// AdmissionOpen reports whether new turns are admitted.
func (service *Service) AdmissionOpen() bool {
	if service.admission == nil {
		return false
	}
	return service.admission.AdmissionOpen()
}

// StopAdmission rejects new turns with [ErrAdmissionUnavailable]. It is
// idempotent and neither waits for nor cancels admitted turns.
func (service *Service) StopAdmission() {
	if service.admission != nil {
		service.admission.CloseAdmission()
	}
}

// Drain closes admission, cancels every active turn, and joins them. A context
// error stops waiting, not the cancellations; admission stays closed.
func (service *Service) Drain(ctx context.Context) error {
	service.StopAdmission()
	service.cancelActiveTurns()
	if service.admission == nil {
		return nil
	}
	return service.admission.Wait(ctx)
}

// acquireTurn reserves one admission slot, or reports that admission is
// closed. A hand-built Service without an admission group counts as closed.
func (service *Service) acquireTurn() (*lifecycle.Reservation, bool) {
	if service.admission == nil {
		return nil, false
	}
	return service.admission.TryAcquire()
}

// trackTurn records one active turn's cancel function for Drain and returns its
// handle.
func (service *Service) trackTurn(cancel context.CancelFunc) uint64 {
	service.turnsMu.Lock()
	defer service.turnsMu.Unlock()
	if service.turns == nil {
		service.turns = make(map[uint64]context.CancelFunc)
	}
	service.nextTurn++
	service.turns[service.nextTurn] = cancel
	return service.nextTurn
}

// untrackTurn forgets a finished turn.
func (service *Service) untrackTurn(id uint64) {
	service.turnsMu.Lock()
	defer service.turnsMu.Unlock()
	delete(service.turns, id)
}

// cancelActiveTurns cancels every registered turn. Cancellation is idempotent,
// so a turn that finished while the snapshot was taken is unaffected.
func (service *Service) cancelActiveTurns() {
	service.turnsMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(service.turns))
	for _, cancel := range service.turns {
		cancels = append(cancels, cancel)
	}
	service.turnsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// acquireConversationTurn waits for the conversation's turn slot and returns
// its release function. Waiting honors ctx, so Drain cancels a queued turn
// instead of letting it start after the running turn ends.
func (service *Service) acquireConversationTurn(ctx context.Context, conversationID string) (func(), error) {
	service.gatesMu.Lock()
	if service.gates == nil {
		service.gates = make(map[string]*conversationGate)
	}
	gate := service.gates[conversationID]
	if gate == nil {
		gate = &conversationGate{token: make(chan struct{}, 1)}
		service.gates[conversationID] = gate
	}
	gate.refs++
	service.gatesMu.Unlock()

	select {
	case gate.token <- struct{}{}:
		return func() { service.releaseConversationTurn(conversationID, gate) }, nil
	case <-ctx.Done():
		service.releaseConversationRef(conversationID, gate)
		return nil, ctx.Err()
	}
}

// releaseConversationTurn frees the slot and forgets this turn's interest in
// the conversation.
func (service *Service) releaseConversationTurn(conversationID string, gate *conversationGate) {
	<-gate.token
	service.releaseConversationRef(conversationID, gate)
}

// releaseConversationRef drops one turn's interest in a conversation gate and
// removes the gate when no turn holds or waits on it.
func (service *Service) releaseConversationRef(conversationID string, gate *conversationGate) {
	service.gatesMu.Lock()
	defer service.gatesMu.Unlock()
	gate.refs--
	if gate.refs == 0 && service.gates[conversationID] == gate {
		delete(service.gates, conversationID)
	}
}

// Conversation is one durable agent conversation.
type Conversation struct {
	ID        string
	CreatedAt time.Time
}

// StoredMessage is one persisted conversation row, decoded for readers.
type StoredMessage struct {
	ID                int64
	Role              string
	Content           string
	ToolCalls         []string
	Calls             []ToolCall
	ToolCallID        string
	ToolName          string
	Artifacts         []Artifact
	ViewContext       json.RawMessage
	AutomationContext json.RawMessage
	CreatedAt         time.Time
}

// Turn is the result of one user message: the assistant's reply text plus the
// tool names the turn called, in order.
type Turn struct {
	Reply     string
	ToolCalls []string
	Artifacts []Artifact
}

// ToolCall is the public projection of a native assistant call.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// CreateConversation opens one durable conversation and returns its ID.
func (service *Service) CreateConversation(ctx context.Context) (Conversation, error) {
	raw, err := uuid.NewV7()
	if err != nil {
		return Conversation{}, fmt.Errorf("agent conversation id: %w", err)
	}
	conversation := Conversation{
		ID:        conversationIDPrefix + strings.ReplaceAll(raw.String(), "-", ""),
		CreatedAt: time.Now().UTC(),
	}
	err = service.queries.CreateConversation(ctx, dbsqlc.CreateConversationParams{
		ID:        conversation.ID,
		CreatedAt: encodeAgentTimestamp(conversation.CreatedAt),
	})
	if err != nil {
		return Conversation{}, fmt.Errorf("agent create conversation: %w", err)
	}
	return conversation, nil
}

// History reads one conversation back oldest-first.
func (service *Service) History(ctx context.Context, conversationID string) ([]StoredMessage, error) {
	if err := service.requireConversation(ctx, conversationID); err != nil {
		return nil, err
	}
	rows, err := service.queries.ListMessages(ctx, dbsqlc.ListMessagesParams{ConversationID: conversationID})
	if err != nil {
		return nil, fmt.Errorf("agent history: %w", err)
	}
	var history []StoredMessage
	for _, row := range rows {
		stored, decodeErr := storedMessageFromRow(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		history = append(history, stored)
	}
	return history, nil
}

// ConversationSummary is one conversation list row with its sidebar display
// fields.
type ConversationSummary struct {
	ID            string
	CreatedAt     time.Time
	MessageCount  int
	LastMessageAt *time.Time
	Preview       string
}

// ListConversations reads every conversation newest-activity-first for the
// dashboard sidebar.
func (service *Service) ListConversations(ctx context.Context) ([]ConversationSummary, error) {
	rows, err := service.queries.ListConversationSummaries(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent list conversations: %w", err)
	}
	var summaries []ConversationSummary
	for _, row := range rows {
		summary, decodeErr := conversationSummaryFromRow(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// SendMessage runs one turn over the conversation without streaming events.
func (service *Service) SendMessage(ctx context.Context, conversationID string, input MessageInput) (Turn, error) {
	return service.SendMessageWithEvents(ctx, conversationID, input, nil)
}

// SendMessageWithEvents runs one turn, reporting turn.started/tool.started/
// tool.finished/turn.finished (or turn.failed) through emit. A nil emit
// disables events; persistence is identical either way, so the blocking POST
// and the stream rebuild the same rows.
func (service *Service) SendMessageWithEvents(
	ctx context.Context,
	conversationID string, messageInput MessageInput,
	emit func(TurnEvent),
) (Turn, error) {
	if err := validateMessageInput(messageInput); err != nil {
		return Turn{}, err
	}
	reservation, admitted := service.acquireTurn()
	if !admitted {
		// Close the streamed turn even if admission closed after the transport
		// pre-check, so an SSE client always sees a terminal event.
		emitTurnFailed(emit, ErrAdmissionUnavailable)
		return Turn{}, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	// Drain cancels this context and joins the runner before releasing admission.
	// Completed messages remain evidence even when a later call is interrupted.
	turnCtx, cancelTurn := context.WithCancel(ctx)
	turnID := service.trackTurn(cancelTurn)
	defer func() {
		service.untrackTurn(turnID)
		cancelTurn()
	}()
	// A turn admitted just before admission closed can register after Drain's
	// cancel sweep; cancel it here so Drain's join is also a cancellation.
	if !service.AdmissionOpen() {
		cancelTurn()
	}
	fail := func(stage string, err error) (Turn, error) {
		if expectedTurnCancellation(turnCtx, err) {
			err = turnCancellationError{err: err}
		} else {
			service.logTurnFailure(turnCtx, conversationID, stage, err)
		}
		emitTurnFailed(emit, err)
		return Turn{}, err
	}
	// Serialize turns within one conversation: two clients submitting at once
	// would otherwise rebuild the same history snapshot and interleave their
	// user, tool, and assistant rows.
	releaseTurn, gateErr := service.acquireConversationTurn(turnCtx, conversationID)
	if gateErr != nil {
		return fail(turnStageQueue, gateErr)
	}
	defer releaseTurn()
	history, err := service.loadModelMessages(turnCtx, conversationID)
	if err != nil {
		return fail(turnStageHistory, err)
	}
	if err = service.checkConversationProvider(turnCtx, conversationID); err != nil {
		return fail(turnStageHistory, err)
	}
	userMessage := newUserMessage(messageInput, service.provider)
	if persistErr := service.persistMessage(turnCtx, conversationID, userMessage); persistErr != nil {
		return fail(turnStagePersist, persistErr)
	}
	if emit != nil {
		emit(TurnEvent{Type: EventTurnStarted})
	}
	input, err := prepareTurnInput(history, userMessage)
	if err != nil {
		return fail(turnStageHistory, err)
	}
	trace := &turnTrace{service: service, conversationID: conversationID, emit: emit}
	agentRunner, err := service.newAgentRunner(turnCtx, trace)
	if err != nil {
		return fail(turnStageRunner, err)
	}
	modelCtx := context.WithValue(turnCtx, modelConversationKey{}, conversationID)
	runErr := trace.failure(consumeRunner(agentRunner.Run(withToolPreparing(modelCtx, trace.preparing), input)))
	if runErr != nil {
		return fail(runnerFailureStage(runErr), runErr)
	}
	if trace.final == nil {
		return fail(turnStageRunner, errors.New("agent turn: empty runner response"))
	}
	turn := Turn{Reply: trace.final.Content, ToolCalls: trace.tools, Artifacts: trace.committedArtifacts()}
	if emit != nil {
		emit(TurnEvent{Type: EventTurnFinished, Reply: turn.Reply, ToolCalls: turn.ToolCalls})
	}
	return turn, nil
}

// emitTurnFailed closes a streamed turn with the opaque text for err, so an SSE
// client always sees a terminal event even when the turn never ran. A nil emit
// disables events.
func emitTurnFailed(emit func(TurnEvent), err error) {
	if emit == nil {
		return
	}
	emit(TurnEvent{Type: EventTurnFailed, Error: turnFailureText(err)})
}

// turnFailureText maps a turn error to client text, mirroring the Huma
// boundary: unknown conversations, closed admission, and cancellation name
// themselves, everything else is opaque.
func turnFailureText(err error) string {
	var invalid messageTextError
	switch {
	case errors.As(err, &invalid):
		return invalid.detail
	case errors.Is(err, ErrConversationNotFound):
		return "agent conversation not found"
	case errors.Is(err, ErrAdmissionUnavailable):
		return "agent admission is unavailable"
	case errors.As(err, &turnCancellationError{}):
		return "agent turn canceled"
	default:
		return "agent turn failed"
	}
}

// ConversationExists reports whether a conversation row exists.
func (service *Service) ConversationExists(ctx context.Context, conversationID string) (bool, error) {
	err := service.requireConversation(ctx, conversationID)
	if errors.Is(err, ErrConversationNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Turn failure stages recorded with agent.turn_failed: the fixed operation
// that failed, so a log reader never needs the raw error text.
const (
	turnStageQueue   = "queue"
	turnStageHistory = "history"
	turnStagePersist = "persist"
	turnStageModel   = "model"
	turnStageTool    = "tool"
	turnStageRunner  = "runner"
)

// turnFailureCode classifies one turn failure for logs. Provider, tool, and
// persistence error text can carry prompts, arguments, or rejected household
// values, so only this fixed code and the stage cross into a log record.
func turnFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrConversationNotFound):
		return "conversation_not_found"
	case errors.Is(err, ErrAdmissionUnavailable):
		return "admission_unavailable"
	case errors.As(err, &tracePersistError{}):
		return "trace_persist_failed"
	case errors.As(err, &toolCallError{}):
		return "tool_call_failed"
	case errors.Is(err, errModelResponseInvalid):
		return "model_response_invalid"
	case errors.As(err, &modelError{}):
		return "model_call_failed"
	default:
		return "turn_failed"
	}
}

// logTurnFailure records the cause server-side only; it never crosses the
// HTTP boundary, mirroring the MCP error-mapping posture. docs/logging.md
// forbids raw error strings here, so the record carries fixed codes instead.
func (service *Service) logTurnFailure(ctx context.Context, conversationID, stage string, err error) {
	attrs := []slog.Attr{
		slog.String("event", "agent.turn_failed"),
		slog.String("conversation_id", conversationID),
		slog.String("stage", stage),
		slog.String("error_code", turnFailureCode(err)),
	}
	if errors.As(err, &modelError{}) {
		attrs = append(attrs, modelFailureAttrs(err)...)
	}
	service.logger.LogAttrs(ctx, slog.LevelError, "agent turn failed", attrs...)
}

// modelFailureAttrs says why a model call failed using fixed values only: the
// provider's HTTP status or the rule the adapter rejected a response for.
// Provider error text can quote prompts or credentials and is never logged.
func modelFailureAttrs(err error) []slog.Attr {
	failure := func(code string) []slog.Attr { return []slog.Attr{slog.String("model_failure", code)} }
	var rejected modelResponseError
	var status *openai.Error
	var streamFailure *ssestream.StreamError
	switch {
	case errors.Is(err, errModelResponseInvalid):
		return failure("empty_response")
	case errors.As(err, &rejected):
		return failure(rejected.reason)
	case errors.As(err, &status):
		return append(failure("provider_status"), slog.Int("provider_status", status.StatusCode))
	case errors.As(err, &streamFailure):
		return failure(providerStreamError)
	case errors.Is(err, context.DeadlineExceeded):
		return failure("deadline_exceeded")
	case errors.Is(err, context.Canceled):
		return failure("canceled")
	default:
		return failure("unclassified")
	}
}

// requireConversation maps a missing conversation row to the 404 sentinel.
func (service *Service) requireConversation(ctx context.Context, conversationID string) error {
	if _, err := service.queries.GetConversation(ctx, dbsqlc.GetConversationParams{ID: conversationID}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConversationNotFound
		}
		return fmt.Errorf("agent conversation: %w", err)
	}
	return nil
}

// historyRow is one prepared model message and its encoded byte cost.
type historyRow struct {
	role    string
	bytes   int
	message *schema.Message
}

// loadModelMessages rebuilds the next turn's input from the newest complete
// turns inside the history budgets. Whole-conversation retention prunes only
// idle conversations, so an active conversation needs this bound of its own:
// without it every turn resends every row and full tool result until the provider
// rejects the request, after which that conversation can never finish a turn.
func (service *Service) loadModelMessages(ctx context.Context, conversationID string) ([]*schema.Message, error) {
	if err := service.requireConversation(ctx, conversationID); err != nil {
		return nil, err
	}
	rows, err := service.queries.ListRecentMessageJson(ctx, dbsqlc.ListRecentMessageJsonParams{
		ConversationID: conversationID,
		Limit:          historyRowBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("agent history: %w", err)
	}
	// Rows arrive newest-first so the budget can be applied without loading the
	// whole conversation.
	decoded := make([]historyRow, 0, len(rows))
	for _, row := range rows {
		message, decodeErr := historyMessageFromRow(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		decoded = append(decoded, message)
	}
	return boundConversationHistory(decoded), nil
}

// boundConversationHistory selects the newest rows inside historyByteBudget and
// returns them oldest-first for the model. It drops any window without a user
// message: the turn appends its own user message, and history that opened with
// a tool result whose assistant tool call was cut would be rejected as an
// incomplete sequence.
func boundConversationHistory(rows []historyRow) []*schema.Message {
	return boundConversationHistoryWithin(rows, historyByteBudget, true)
}

// prepareTurnInput includes the prepared current message in the existing
// history budget, without truncating user data to make it fit.
func prepareTurnInput(history []*schema.Message, userMessage *schema.Message) ([]*schema.Message, error) {
	prepared := prepareModelMessage(userMessage)
	encoded, err := json.Marshal(prepared)
	if err != nil {
		return nil, fmt.Errorf("agent model input: %w", err)
	}
	if len(encoded) > historyByteBudget {
		return nil, messageTextError{detail: "agent model input exceeds history budget"}
	}
	history, err = fitModelHistory(history, historyByteBudget-len(encoded))
	if err != nil {
		return nil, err
	}
	return append(history, prepared), nil
}

// fitModelHistory reserves the current user message's encoded cost before
// selecting whole prior turns; it never truncates a message or its context.
func fitModelHistory(history []*schema.Message, remaining int) ([]*schema.Message, error) {
	rows := make([]historyRow, 0, len(history))
	for _, message := range slices.Backward(history) {
		raw, err := json.Marshal(message)
		if err != nil {
			return nil, fmt.Errorf("agent history: %w", err)
		}
		rows = append(rows, historyRow{role: string(message.Role), bytes: len(raw), message: message})
	}
	return boundConversationHistoryWithin(rows, remaining, false), nil
}

func boundConversationHistoryWithin(rows []historyRow, budget int, allowOversizedFirst bool) []*schema.Message {
	kept := 0
	var bytes int
	for kept < len(rows) {
		next := bytes + rows[kept].bytes
		if next > budget && (!allowOversizedFirst || kept > 0) {
			break
		}
		bytes = next
		kept++
	}
	start := -1
	for index := range kept {
		if rows[index].role == string(schema.User) {
			start = index
		}
	}
	if start < 0 {
		return nil
	}
	history := make([]*schema.Message, 0, start+1)
	for index := start; index >= 0; index-- {
		history = append(history, rows[index].message)
	}
	return history
}

// persistMessage appends one message to a conversation.
func (service *Service) persistMessage(ctx context.Context, conversationID string, message *schema.Message) error {
	_, err := service.persistMessageWithID(ctx, conversationID, message)
	return err
}

// persistMessageWithID returns the row identity from the insertion itself.
func (service *Service) persistMessageWithID(
	ctx context.Context,
	conversationID string,
	message *schema.Message,
) (int64, error) {
	raw, err := json.Marshal(message)
	if err != nil {
		return 0, fmt.Errorf("agent persist message: %w", err)
	}
	id, err := service.queries.InsertMessage(ctx, dbsqlc.InsertMessageParams{
		ConversationID: conversationID,
		Role:           string(message.Role),
		MessageJson:    string(raw),
		CreatedAt:      encodeAgentTimestamp(time.Now()),
	})
	if err != nil {
		return 0, fmt.Errorf("agent persist message: %w", err)
	}
	return id, nil
}
