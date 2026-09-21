package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSendMessageRejectsUnusableText protects the shared message-text contract
// behind both transports: blank-after-trim and over-long text are client errors
// that must not reach the model or persist a row. It fails if the service
// accepts either, or if the bound counts bytes instead of characters.
func TestSendMessageRejectsUnusableText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		text    string
		wantErr bool
	}{
		{name: "blank", text: "   ", wantErr: true},
		{name: "empty", text: "", wantErr: true},
		{name: "one over the limit", text: strings.Repeat("a", maxMessageRunes+1), wantErr: true},
		{
			name: "accented characters at the limit",
			text: strings.Repeat("é", maxMessageRunes),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			service := newTurnService(t, failingChatModel{err: errors.New("model must not be reached")})
			conversation := mustCreateConversation(t, service)

			_, err := service.SendMessage(context.Background(), conversation.ID, testCase.text)
			var invalid messageTextError
			if got := errors.As(err, &invalid); got != testCase.wantErr {
				t.Fatalf("error = %v, want messageTextError = %t", err, testCase.wantErr)
			}
			history, historyErr := service.History(context.Background(), conversation.ID)
			if historyErr != nil {
				t.Fatal(historyErr)
			}
			if testCase.wantErr {
				if len(history) != 0 {
					t.Fatalf("rejected text persisted %d rows, want none", len(history))
				}
				return
			}
			// Accepted text proceeds to the model, so the failing model leaves
			// exactly the persisted user row behind.
			if len(history) != 1 || history[0].Role != "user" {
				t.Fatalf("accepted history = %+v, want only the user message", history)
			}
		})
	}
}
