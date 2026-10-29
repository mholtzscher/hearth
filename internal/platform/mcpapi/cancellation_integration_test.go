//go:build integration

package mcpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationsmcp "github.com/mholtzscher/hearth/internal/modules/notifications/mcpapi"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestMCPDisconnectDoesNotDiagnoseAnInternalFailure(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"generic_handler", "notification_pre_admission"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			server := mcpapi.New(mcpapi.Config{
				Name: "cancellation-test", Version: "1",
				Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
			})
			started := make(chan struct{})
			name, arguments := registerCancellationTool(t, server, started, kind)
			returned := make(chan struct{})
			var finished sync.Once
			underlying := server.HTTPHandler()
			wrapped := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				method := cancellationRequestMethod(t, request)
				underlying.ServeHTTP(writer, request)
				if method == "tools/call" {
					finished.Do(func() { close(returned) })
				}
			})
			client := connectSession(t, wrapped)
			if version := client.InitializeResult().ProtocolVersion; version < "2026-07-28" {
				t.Fatalf("request cancellation requires protocol >= 2026-07-28, negotiated %q", version)
			}
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() {
				_, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("tool never entered")
			}
			cancel()
			select {
			case <-returned:
			case <-time.After(5 * time.Second):
				t.Fatal("server request did not finish after disconnect")
			}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("client error = %v, want caller cancellation", err)
			}
			if logs.Len() != 0 {
				t.Errorf("real MCP caller disconnect emitted a failure diagnostic: %s", logs.String())
			}
		})
	}
}

// Identify the actual tool request so an initialization response cannot satisfy
// the barrier before tool middleware has finished.
func cancellationRequestMethod(t *testing.T, request *http.Request) string {
	t.Helper()
	if request.Body == nil {
		return ""
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		t.Errorf("read MCP fixture request: %v", err)
		return ""
	}
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(raw))
	var message struct {
		Method string `json:"method"`
	}
	if len(raw) > 0 {
		if decodeErr := json.Unmarshal(raw, &message); decodeErr != nil {
			t.Errorf("decode MCP fixture request: %v", decodeErr)
		}
	}
	return message.Method
}

func registerCancellationTool(
	t *testing.T,
	server *mcpapi.Server,
	started chan struct{},
	kind string,
) (string, map[string]any) {
	t.Helper()
	if kind == "generic_handler" {
		mcpapi.Register(server, mcpapi.Tool[noInput, greetOutput]{
			Name: "cancel_before_work",
			Handler: func(ctx context.Context, _ noInput) (greetOutput, error) {
				close(started)
				<-ctx.Done()
				return greetOutput{}, ctx.Err()
			},
		})
		return "cancel_before_work", map[string]any{}
	}
	repo := &cancellationRepository{}
	sender := &cancellationSender{}
	service, err := notifications.NewService(t.Context(), repo, sender)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if repo.admissions.Load() != 0 || sender.calls.Load() != 0 {
			t.Errorf(
				"pre-admission cancellation admitted=%d dispatched=%d",
				repo.admissions.Load(),
				sender.calls.Load(),
			)
		}
	})
	notificationsmcp.Register(server, &cancelBeforeNotificationAdmission{Service: service, started: started})
	destination, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	return "send_notification", map[string]any{"destination_id": string(destination), "message": "synthetic request"}
}

// The barrier supplies cancellation before the real service's admission check.
// No send is admitted, so no durable notification work is abandoned.
type cancelBeforeNotificationAdmission struct {
	*notifications.Service

	started chan struct{}
}

func (service *cancelBeforeNotificationAdmission) SendPublic(
	ctx context.Context,
	in notifications.SendInput,
) (notifications.Attempt, error) {
	close(service.started)
	<-ctx.Done()
	return service.Service.SendPublic(ctx, in)
}

type cancellationRepository struct {
	notifications.Repository

	admissions atomic.Int32
}

func (*cancellationRepository) Recover(context.Context, time.Time) error { return nil }

func (repo *cancellationRepository) Admit(
	context.Context,
	notifications.SendInput,
	time.Time,
	time.Time,
) (notifications.Attempt, bool, error) {
	repo.admissions.Add(1)
	return notifications.Attempt{}, false, errors.New("unexpected admission after cancellation")
}

type cancellationSender struct{ calls atomic.Int32 }

func (sender *cancellationSender) Send(context.Context, notifications.Attempt) notifications.Evidence {
	sender.calls.Add(1)
	return notifications.Evidence{Outcome: notifications.Accepted}
}
