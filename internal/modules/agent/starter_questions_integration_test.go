//go:build integration

package agent //nolint:testpackage // Tests construct the service with an injected fake model.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/household"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// starterModel answers each Generate with the next scripted reply or error
// and records the messages and conversation identity it received.
type starterModel struct {
	mutex    sync.Mutex
	replies  []starterModelReply
	requests [][]*schema.Message
	sessions []string
}

type starterModelReply struct {
	content string
	err     error
}

func (fake *starterModel) Generate(
	ctx context.Context,
	messages []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	fake.requests = append(fake.requests, messages)
	session, _ := ctx.Value(modelConversationKey{}).(string)
	fake.sessions = append(fake.sessions, session)
	if len(fake.replies) == 0 {
		return nil, errors.New("starterModel: no scripted reply")
	}
	reply := fake.replies[0]
	fake.replies = fake.replies[1:]
	if reply.err != nil {
		return nil, reply.err
	}
	return schema.AssistantMessage(reply.content, nil), nil
}

func (fake *starterModel) Stream(
	context.Context, []*schema.Message, ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("starterModel: Stream is not supported")
}

func (fake *starterModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return fake, nil
}

func (fake *starterModel) userMessages() []string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	out := make([]string, len(fake.requests))
	for index, request := range fake.requests {
		out[index] = request[len(request)-1].Content
	}
	return out
}

type fakeDigestReader struct {
	digest HouseholdDigest
	err    error
	calls  int
}

func (reader *fakeDigestReader) ReadHouseholdDigest(context.Context) (HouseholdDigest, error) {
	reader.calls++
	return reader.digest, reader.err
}

func newStarterService(t *testing.T, chatModel *starterModel, reader HouseholdDigestReader) *Service {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, err := NewService(context.Background(), Config{
		DB:              database,
		Tools:           []tool.BaseTool{stubTool{name: "noop"}},
		ChatModel:       chatModel,
		Retention:       MinimumConversationRetention,
		HouseholdDigest: reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func starterPassTime() time.Time {
	return time.Date(2026, 11, 24, 19, 42, 0, 0, time.FixedZone("CST", -6*60*60))
}

func validStarterReply(prefix string) string {
	return starterReply(
		question(prefix+" Which devices are offline?"),
		question(prefix+" Show the temperatures"),
		question(prefix+" What happened overnight?"),
		question(prefix+" Why did Porch lights fail?"),
		question(prefix+" Show humidity today"),
		question(prefix+" Is the garage door open?"),
	)
}

// refreshedStarterService runs two passes, answering "A" then "B" questions,
// over a household with one unavailable lock and one stored conversation.
func refreshedStarterService(t *testing.T) (*Service, *starterModel, StarterQuestions) {
	t.Helper()
	chat := &starterModel{}
	reader := &fakeDigestReader{digest: HouseholdDigest{
		LocalTime: starterPassTime(), UnitSystem: household.UnitSystemUS,
		Entities: []DigestEntity{{Name: "Garage Lock", Type: "hearth.lock/v1", Availability: DigestUnavailable}},
	}}
	service := newStarterService(t, chat, reader)
	ctx := context.Background()
	// With no scripted reply the turn fails after storing the user message,
	// which is all the conversation preview needs.
	conversation := mustCreateConversation(t, service)
	if _, err := service.SendMessage(ctx, conversation.ID, MessageInput{Text: "Why is the garage cold?"}); err == nil {
		t.Fatal("turn unexpectedly succeeded")
	}
	chat.mutex.Lock()
	chat.requests, chat.sessions = nil, nil
	chat.replies = []starterModelReply{{content: validStarterReply("A")}, {content: validStarterReply("B")}}
	chat.mutex.Unlock()
	if got := service.StarterQuestions(); len(got.Questions) != 0 || !got.GeneratedAt.IsZero() {
		t.Fatalf("cache before first pass = %+v", got)
	}
	if err := service.RefreshStarterQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	first := service.StarterQuestions()
	if err := service.RefreshStarterQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	return service, chat, first
}

func TestRefreshStarterQuestionsCachesValidReply(t *testing.T) {
	t.Parallel()
	service, _, first := refreshedStarterService(t)
	if len(first.Questions) != maxStarterQuestions || first.Questions[0].Title != "A Which devices are offline?" {
		t.Fatalf("cache = %+v", first)
	}
	if !first.GeneratedAt.Equal(starterPassTime()) || first.GeneratedAt.Location() != time.UTC {
		t.Fatalf("GeneratedAt = %v, want %v in UTC", first.GeneratedAt, starterPassTime())
	}
	if got := service.StarterQuestions().Questions[0].Title; got != "B Which devices are offline?" {
		t.Fatalf("second pass title = %q", got)
	}
}

func TestRefreshStarterQuestionsSendsDigestAndPreviousQuestions(t *testing.T) {
	t.Parallel()
	_, chat, _ := refreshedStarterService(t)
	messages := chat.userMessages()
	if len(messages) != 2 {
		t.Fatalf("model calls = %d, want 2", len(messages))
	}
	for _, want := range []string{"Garage Lock [hearth.lock/v1]", "- Why is the garage cold?", "Unit system: us"} {
		if !strings.Contains(messages[0], want) {
			t.Fatalf("first digest lacks %q:\n%s", want, messages[0])
		}
	}
	if strings.Contains(messages[0], "Previous starter questions") {
		t.Fatalf("first digest lists previous questions:\n%s", messages[0])
	}
	if !strings.Contains(messages[1], "Previous starter questions:\n- A Which devices are offline?") {
		t.Fatalf("second digest lacks the previous questions:\n%s", messages[1])
	}
	chat.mutex.Lock()
	defer chat.mutex.Unlock()
	for _, session := range chat.sessions {
		if session != starterQuestionModelConversation {
			t.Fatalf("model conversation = %q", session)
		}
	}
	if chat.requests[0][0].Role != schema.System || chat.requests[0][0].Content != starterQuestionInstruction {
		t.Fatal("first message is not the starter question instruction")
	}
}

func TestRefreshStarterQuestionsKeepsCacheOnFailure(t *testing.T) {
	t.Parallel()
	providerErr := errors.New("provider rejected the request")
	digestErr := errors.New("devices unavailable")
	cases := map[string]struct {
		reply     starterModelReply
		digestErr error
		stage     string
		wantCause error
	}{
		"model error": {
			reply:     starterModelReply{err: providerErr},
			stage:     StarterQuestionStageModel,
			wantCause: providerErr,
		},
		"malformed": {reply: starterModelReply{content: "no json here"}, stage: StarterQuestionStageReply},
		"too few valid": {
			reply: starterModelReply{
				content: starterReply(question("Show temperatures"), question("Turn on the lights")),
			},
			stage: StarterQuestionStageReply,
		},
		"digest failure": {digestErr: digestErr, stage: StarterQuestionStageDigestRead, wantCause: digestErr},
	}
	for name, test := range cases {
		chat := &starterModel{replies: []starterModelReply{{content: validStarterReply("A")}, test.reply}}
		reader := &fakeDigestReader{digest: HouseholdDigest{LocalTime: starterPassTime()}}
		service := newStarterService(t, chat, reader)
		if err := service.RefreshStarterQuestions(context.Background()); err != nil {
			t.Fatalf("%s: first pass: %v", name, err)
		}
		before := service.StarterQuestions()
		reader.err = test.digestErr
		err := service.RefreshStarterQuestions(context.Background())
		refreshErr, ok := errors.AsType[*StarterQuestionRefreshError](err)
		if !ok || refreshErr.Stage != test.stage {
			t.Fatalf("%s: err = %v, want stage %s", name, err, test.stage)
		}
		if test.wantCause != nil && !errors.Is(err, test.wantCause) {
			t.Fatalf("%s: err = %v does not wrap %v", name, err, test.wantCause)
		}
		after := service.StarterQuestions()
		if after.Questions[0] != before.Questions[0] || !after.GeneratedAt.Equal(before.GeneratedAt) {
			t.Fatalf("%s: cache changed from %+v to %+v", name, before, after)
		}
	}
}

func TestRefreshStarterQuestionsSkipsWhenAdmissionClosed(t *testing.T) {
	t.Parallel()
	chat := &starterModel{}
	reader := &fakeDigestReader{}
	service := newStarterService(t, chat, reader)
	service.StopAdmission()
	if err := service.RefreshStarterQuestions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 0 || len(chat.userMessages()) != 0 {
		t.Fatalf(
			"closed admission read the digest %d times and called the model %d times",
			reader.calls,
			len(chat.userMessages()),
		)
	}
}

func TestRefreshStarterQuestionsRequiresDigestReader(t *testing.T) {
	t.Parallel()
	service := newStarterService(t, &starterModel{}, nil)
	err := service.RefreshStarterQuestions(context.Background())
	if refreshErr, ok := errors.AsType[*StarterQuestionRefreshError](
		err,
	); !ok ||
		refreshErr.Stage != StarterQuestionStageUnconfigured {
		t.Fatalf("err = %v, want the unconfigured stage", err)
	}
}
