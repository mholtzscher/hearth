package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/household"
)

const (
	// maxStarterQuestionTitleRunes bounds the chip text.
	maxStarterQuestionTitleRunes = 60
	// maxStarterQuestionPromptRunes bounds the message a tapped chip sends.
	maxStarterQuestionPromptRunes = 240
	// minStarterQuestions is the fewest valid questions a reply must yield to
	// replace the cache; fewer keeps the previous questions.
	minStarterQuestions = 3
	// maxStarterQuestions is how many questions the cache keeps.
	maxStarterQuestions = 4
	// recentConversationPreviews is how many conversation previews the digest
	// includes, newest first.
	recentConversationPreviews = 8
	// defaultStarterQuestionPassTimeout bounds one pass, including the model
	// call. A timed-out pass keeps the previous questions.
	defaultStarterQuestionPassTimeout = 90 * time.Second
	// starterQuestionModelConversation names the provider conversation for
	// generation requests. Providers that route by conversation require one.
	starterQuestionModelConversation = "starter-questions"
)

// StarterQuestion is one suggested prompt for an empty conversation. Title is
// the chip text; Prompt is the message sent when the chip is tapped.
type StarterQuestion struct {
	Title  string
	Prompt string
}

// StarterQuestions is the current cache. GeneratedAt is zero before the first
// successful pass, and Questions is then empty. Enabled reports whether
// generation is on, so a manual refresh can run.
type StarterQuestions struct {
	Questions   []StarterQuestion
	GeneratedAt time.Time
	Enabled     bool
}

// HouseholdDigestReader reads the household snapshot one generation pass uses.
// Core assembly implements it over Devices and Automations, so this module
// does not import them.
type HouseholdDigestReader interface {
	ReadHouseholdDigest(ctx context.Context) (HouseholdDigest, error)
}

// HouseholdDigest is the household snapshot one generation pass reads.
type HouseholdDigest struct {
	// LocalTime is the pass time in the household location.
	LocalTime  time.Time
	UnitSystem household.UnitSystem
	// Entities holds enabled Entities with known availability.
	Entities    []DigestEntity
	Automations []DigestAutomation
}

// Entity availability values a digest carries. The reader omits Entities whose
// availability is unknown.
const (
	DigestAvailable   = "available"
	DigestUnavailable = "unavailable"
)

// DigestEntity is one enabled Entity, projected to household units.
type DigestEntity struct {
	// Name is the name override when set, otherwise the Entity name.
	Name string
	// DeviceID identifies the owning Device; Device is its display name.
	DeviceID string
	Device   string
	// DeviceKind is the owning Device's kind, such as lock or sensor.
	DeviceKind string
	// Area is the name of the Entity's effective Area; empty when it has none.
	Area string
	// Type is the Entity type ID, such as hearth.temperature/v1.
	Type         string
	Availability string
	// State is the household-unit read projection; nil when the Entity has no State.
	State json.RawMessage
	// Units maps a State field name to its unit label, such as "°F".
	Units map[string]string
}

// DigestAutomation is one Automation with its problems from the last 24 hours.
type DigestAutomation struct {
	Name    string
	Enabled bool
	// Problems is newest first, at most three.
	Problems []DigestAutomationProblem
}

// Automation problem kinds a digest reports.
const (
	DigestProblemFailed            = "failed"
	DigestProblemInterrupted       = "interrupted"
	DigestProblemConditionsUnknown = "conditions_unknown"
)

// DigestAutomationProblem is one failed or interrupted run, or one skip
// because conditions could not be evaluated.
type DigestAutomationProblem struct {
	At   time.Time
	Kind string
}

// Stages a [StarterQuestionRefreshError] reports. They are fixed so callers can
// log them without the underlying error text.
const (
	StarterQuestionStageDigestRead       = "digest_read"
	StarterQuestionStageConversationRead = "conversation_read"
	StarterQuestionStageModel            = "model"
	StarterQuestionStageReply            = "reply"
	StarterQuestionStageTimeout          = "timeout"
)

// StarterQuestionRefreshError reports which stage of a generation pass failed.
// The cache is unchanged after any such error.
type StarterQuestionRefreshError struct {
	Stage string
	err   error
}

func (refreshErr *StarterQuestionRefreshError) Error() string {
	return "starter questions " + refreshErr.Stage + ": " + refreshErr.err.Error()
}

func (refreshErr *StarterQuestionRefreshError) Unwrap() error { return refreshErr.err }

func starterQuestionError(stage string, err error) error {
	return &StarterQuestionRefreshError{Stage: stage, err: err}
}

// StarterQuestions returns the current cache. It never blocks on a pass.
func (service *Service) StarterQuestions() StarterQuestions {
	current := StarterQuestions{}
	if cached := service.starterQuestions.Load(); cached != nil {
		current = *cached
	}
	current.Enabled = service.householdDigest != nil
	return current
}

// ErrStarterQuestionsOff reports that starter question generation is turned
// off, so no household digest reader is configured.
var ErrStarterQuestionsOff = errors.New("starter questions are off")

// starterQuestionRefresh is one in-flight pass that later callers join.
type starterQuestionRefresh struct {
	done chan struct{}
	err  error
}

// RefreshStarterQuestions runs one generation pass, or joins the pass already
// running and returns its result, so concurrent scheduled and manual requests
// share one model call. A pass reads the household digest and recent
// conversations, asks the model for questions without tools under its own
// deadline, and replaces the cache when at least three valid questions
// survive. It returns ErrAdmissionUnavailable when admission is closed and
// ErrStarterQuestionsOff when generation is off. A pass holds no admission
// reservation, so Drain does not wait for it; the leading caller owns its
// lifetime through ctx. On any error the cache is unchanged.
func (service *Service) RefreshStarterQuestions(ctx context.Context) error {
	if !service.AdmissionOpen() {
		return ErrAdmissionUnavailable
	}
	if service.householdDigest == nil {
		return ErrStarterQuestionsOff
	}
	service.starterMu.Lock()
	if running := service.starterRefresh; running != nil {
		service.starterMu.Unlock()
		select {
		case <-running.done:
			return running.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	refresh := &starterQuestionRefresh{done: make(chan struct{})}
	service.starterRefresh = refresh
	service.starterMu.Unlock()

	refresh.err = service.runStarterQuestionPass(ctx)
	service.starterMu.Lock()
	service.starterRefresh = nil
	service.starterMu.Unlock()
	close(refresh.done)
	return refresh.err
}

func (service *Service) runStarterQuestionPass(ctx context.Context) error {
	passContext, cancel := context.WithTimeout(ctx, service.starterPassTimeout)
	defer cancel()
	err := service.generateStarterQuestions(passContext)
	if err != nil && ctx.Err() == nil && errors.Is(passContext.Err(), context.DeadlineExceeded) {
		return starterQuestionError(StarterQuestionStageTimeout, err)
	}
	return err
}

func (service *Service) generateStarterQuestions(ctx context.Context) error {
	digest, err := service.householdDigest.ReadHouseholdDigest(ctx)
	if err != nil {
		return starterQuestionError(StarterQuestionStageDigestRead, err)
	}
	conversations, err := service.ListConversations(ctx)
	if err != nil {
		return starterQuestionError(StarterQuestionStageConversationRead, err)
	}
	previews := make([]string, 0, recentConversationPreviews)
	for _, conversation := range conversations {
		if len(previews) == recentConversationPreviews {
			break
		}
		if conversation.Preview != "" {
			previews = append(previews, conversation.Preview)
		}
	}
	previous := service.StarterQuestions().Questions
	messages := []*schema.Message{
		schema.SystemMessage(starterQuestionInstruction),
		schema.UserMessage(renderHouseholdDigest(digest, previews, previous)),
	}
	response, err := service.chatModel.Generate(WithModelConversation(ctx, starterQuestionModelConversation), messages)
	if err != nil {
		return starterQuestionError(StarterQuestionStageModel, err)
	}
	// A model may return neither a reply nor an error; turns reject that at the
	// model boundary, and a scheduled pass must not dereference it.
	if response == nil {
		return starterQuestionError(StarterQuestionStageModel, errModelResponseInvalid)
	}
	questions, err := parseStarterQuestions(response.Content)
	if err != nil {
		return starterQuestionError(StarterQuestionStageReply, err)
	}
	service.starterQuestions.Store(&StarterQuestions{Questions: questions, GeneratedAt: digest.LocalTime.UTC()})
	return nil
}

// parseStarterQuestions reads the model reply, tolerating a surrounding code
// fence, and keeps the first four valid questions in reply order. It fails when
// fewer than three are valid.
func parseStarterQuestions(content string) ([]StarterQuestion, error) {
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return nil, errors.New("reply has no JSON object")
	}
	var body struct {
		StarterQuestions []struct {
			Title  string `json:"title"`
			Prompt string `json:"prompt"`
		} `json:"starter_questions"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &body); err != nil {
		return nil, errors.New("reply is not valid JSON")
	}
	seen := make(map[string]bool)
	questions := make([]StarterQuestion, 0, maxStarterQuestions)
	for _, item := range body.StarterQuestions {
		question := StarterQuestion{Title: strings.TrimSpace(item.Title), Prompt: strings.TrimSpace(item.Prompt)}
		key := strings.ToLower(question.Title)
		if !validStarterQuestion(question) || seen[key] {
			continue
		}
		seen[key] = true
		questions = append(questions, question)
		if len(questions) == maxStarterQuestions {
			break
		}
	}
	if len(questions) < minStarterQuestions {
		return nil, fmt.Errorf("reply has %d valid questions, need %d", len(questions), minStarterQuestions)
	}
	return questions, nil
}

func validStarterQuestion(question StarterQuestion) bool {
	titleRunes := utf8.RuneCountInString(question.Title)
	promptRunes := utf8.RuneCountInString(question.Prompt)
	return titleRunes > 0 && titleRunes <= maxStarterQuestionTitleRunes &&
		promptRunes > 0 && promptRunes <= maxStarterQuestionPromptRunes &&
		!startsWithCommandVerb(question.Title) && !startsWithCommandVerb(question.Prompt)
}

// isStarterCommandVerb reports whether a first word reads as a request to
// change a device. A tapped starter question is sent immediately, so one of
// these would run a Command from text the person never wrote.
func isStarterCommandVerb(word string) bool {
	switch word {
	case "turn", "switch", "set", "lock", "unlock", "open", "close", "start", "stop", "play",
		"pause", "resume", "skip", "run", "trigger", "dim", "brighten", "arm", "disarm", "enable",
		"disable", "raise", "lower", "toggle", "mute", "unmute", "activate", "deactivate", "change",
		"adjust", "increase", "decrease":
		return true
	default:
		return false
	}
}

func startsWithCommandVerb(text string) bool {
	words := strings.Fields(text)
	if len(words) == 0 {
		return false
	}
	first := strings.TrimFunc(words[0], func(r rune) bool { return !unicode.IsLetter(r) })
	return isStarterCommandVerb(strings.ToLower(first))
}
