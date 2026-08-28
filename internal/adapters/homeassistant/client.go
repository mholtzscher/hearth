package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const maxMessageSize = 4 << 20

type AuthenticationError struct {
	Message string
}

func (err *AuthenticationError) Error() string {
	if err.Message == "" {
		return "Home Assistant authentication failed"
	}
	return "Home Assistant authentication failed: " + err.Message
}

type upstreamState struct {
	EntityID    string `json:"entity_id"`
	State       string `json:"state"`
	LastUpdated string `json:"last_updated"`
}

type stateChange struct {
	State      upstreamState
	ReceivedAt time.Time
	Sequence   int64
}

type requestMessage struct {
	ID        int64          `json:"id"`
	Type      string         `json:"type"`
	EventType string         `json:"event_type,omitempty"`
	Domain    string         `json:"domain,omitempty"`
	Service   string         `json:"service,omitempty"`
	Target    *serviceTarget `json:"target,omitempty"`
}

type serviceTarget struct {
	EntityID string `json:"entity_id"`
}

type resultMessage struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Success     bool            `json:"success"`
	Result      json.RawMessage `json:"result"`
	Error       *upstreamError  `json:"error,omitempty"`
	received    time.Time
	priorEvents int64
}

type upstreamError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type eventMessage struct {
	Type  string `json:"type"`
	Event struct {
		Data struct {
			EntityID string         `json:"entity_id"`
			NewState *upstreamState `json:"new_state"`
		} `json:"data"`
	} `json:"event"`
}

type client struct {
	connection *websocket.Conn
	entityID   string
	nextID     atomic.Int64
	eventCount atomic.Int64

	mutex   sync.Mutex
	pending map[int64]chan resultMessage
	err     error

	stateMutex   sync.Mutex
	latestStates map[string]stateChange
	stateSignal  chan struct{}

	eventInput chan stateChange
	events     chan stateChange
	done       chan struct{}
	stopOnce   sync.Once
}

func dialClient(ctx context.Context, rawURL, token, entityID string) (*client, error) {
	address, err := websocketAddress(rawURL)
	if err != nil {
		return nil, err
	}
	connection, response, err := websocket.Dial(ctx, address, nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("connect to Home Assistant WebSocket: %w", err)
	}
	connection.SetReadLimit(maxMessageSize)
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = connection.CloseNow()
		}
	}()

	var authentication struct {
		Type    string `json:"type"`
		Message string `json:"message,omitempty"`
	}
	if err := wsjson.Read(ctx, connection, &authentication); err != nil {
		return nil, fmt.Errorf("read Home Assistant authentication challenge: %w", err)
	}
	if authentication.Type != "auth_required" {
		return nil, fmt.Errorf("unexpected Home Assistant authentication message %q", authentication.Type)
	}
	if err := wsjson.Write(ctx, connection, struct {
		Type        string `json:"type"`
		AccessToken string `json:"access_token"`
	}{Type: "auth", AccessToken: token}); err != nil {
		return nil, fmt.Errorf("send Home Assistant authentication: %w", err)
	}
	if err := wsjson.Read(ctx, connection, &authentication); err != nil {
		return nil, fmt.Errorf("read Home Assistant authentication result: %w", err)
	}
	switch authentication.Type {
	case "auth_ok":
	case "auth_invalid":
		return nil, &AuthenticationError{Message: authentication.Message}
	default:
		return nil, fmt.Errorf("unexpected Home Assistant authentication result %q", authentication.Type)
	}

	value := &client{
		connection:   connection,
		entityID:     entityID,
		pending:      make(map[int64]chan resultMessage),
		latestStates: make(map[string]stateChange),
		stateSignal:  make(chan struct{}),
		eventInput:   make(chan stateChange),
		events:       make(chan stateChange),
		done:         make(chan struct{}),
	}
	closeOnError = false
	go value.relayEvents()
	go value.read(ctx)
	return value, nil
}

func websocketAddress(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse Home Assistant URL: %w", err)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("home assistant URL must use HTTP or WebSocket")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("home assistant URL must be absolute")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, "/api/websocket") {
		path += "/api/websocket"
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (client *client) SubscribeStateChanges(ctx context.Context) error {
	_, err := client.request(ctx, requestMessage{Type: "subscribe_events", EventType: "state_changed"})
	if err != nil {
		return fmt.Errorf("subscribe to Home Assistant state changes: %w", err)
	}
	return nil
}

func (client *client) GetStates(ctx context.Context) ([]upstreamState, time.Time, int64, error) {
	result, err := client.request(ctx, requestMessage{Type: "get_states"})
	if err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("get Home Assistant states: %w", err)
	}
	var states []upstreamState
	if err := json.Unmarshal(result.Result, &states); err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("decode Home Assistant states: %w", err)
	}
	return states, result.received, result.priorEvents, nil
}

func (client *client) CallLightService(ctx context.Context, service, entityID string) error {
	_, err := client.request(ctx, requestMessage{
		Type: "call_service", Domain: "light", Service: service,
		Target: &serviceTarget{EntityID: entityID},
	})
	if err != nil {
		return fmt.Errorf("call Home Assistant light service: %w", err)
	}
	return nil
}

func (client *client) Events() <-chan stateChange { return client.events }
func (client *client) Done() <-chan struct{}      { return client.done }

func (client *client) EventSequence() int64 { return client.eventCount.Load() }

func (client *client) WaitForStateAfter(ctx context.Context, sequence int64, state string) (stateChange, error) {
	for {
		client.stateMutex.Lock()
		matching := client.latestStates[state]
		signal := client.stateSignal
		client.stateMutex.Unlock()
		if matching.Sequence > sequence {
			return matching, nil
		}
		select {
		case <-signal:
		case <-client.done:
			return stateChange{}, client.Err()
		case <-ctx.Done():
			return stateChange{}, ctx.Err()
		}
	}
}

func (client *client) Err() error {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return client.err
}

func (client *client) Close() {
	_ = client.connection.CloseNow()
	client.stop(errors.New("home assistant connection closed"))
}

func (client *client) request(ctx context.Context, request requestMessage) (resultMessage, error) {
	request.ID = client.nextID.Add(1)
	response := make(chan resultMessage, 1)
	client.mutex.Lock()
	select {
	case <-client.done:
		err := client.err
		client.mutex.Unlock()
		return resultMessage{}, err
	default:
	}
	client.pending[request.ID] = response
	client.mutex.Unlock()

	payload, err := json.Marshal(request)
	if err != nil {
		client.removePending(request.ID)
		return resultMessage{}, fmt.Errorf("encode Home Assistant request: %w", err)
	}
	if err := client.connection.Write(ctx, websocket.MessageText, payload); err != nil {
		client.removePending(request.ID)
		return resultMessage{}, fmt.Errorf("write Home Assistant request: %w", err)
	}
	result, err := client.waitForResult(ctx, response)
	if err != nil {
		client.removePending(request.ID)
		return resultMessage{}, err
	}
	if !result.Success {
		if result.Error == nil {
			return resultMessage{}, errors.New("request to Home Assistant failed")
		}
		return resultMessage{}, fmt.Errorf(
			"request to Home Assistant failed (%s): %s",
			result.Error.Code,
			result.Error.Message,
		)
	}
	return result, nil
}

func (client *client) waitForResult(ctx context.Context, response <-chan resultMessage) (resultMessage, error) {
	receive := func() (resultMessage, bool) {
		select {
		case result := <-response:
			return result, true
		default:
			return resultMessage{}, false
		}
	}
	if result, ok := receive(); ok {
		return result, nil
	}
	select {
	case result := <-response:
		return result, nil
	case <-ctx.Done():
		if result, ok := receive(); ok {
			return result, nil
		}
		return resultMessage{}, ctx.Err()
	case <-client.done:
		if result, ok := receive(); ok {
			return result, nil
		}
		return resultMessage{}, client.Err()
	}
}

func (client *client) read(ctx context.Context) {
	for {
		_, payload, err := client.connection.Read(ctx)
		if err != nil {
			client.stop(fmt.Errorf("read Home Assistant WebSocket: %w", err))
			return
		}
		var header struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &header); err != nil {
			client.stop(fmt.Errorf("decode Home Assistant message: %w", err))
			return
		}
		switch header.Type {
		case "result":
			var result resultMessage
			if err := json.Unmarshal(payload, &result); err != nil {
				client.stop(fmt.Errorf("decode Home Assistant result: %w", err))
				return
			}
			result.received = time.Now().UTC()
			result.priorEvents = client.eventCount.Load()
			client.deliverResult(result)
		case "event":
			var event eventMessage
			if err := json.Unmarshal(payload, &event); err != nil {
				client.stop(fmt.Errorf("decode Home Assistant event: %w", err))
				return
			}
			if event.Event.Data.EntityID != client.entityID || event.Event.Data.NewState == nil ||
				event.Event.Data.NewState.EntityID != client.entityID {
				continue
			}
			change := stateChange{
				State: *event.Event.Data.NewState, ReceivedAt: time.Now().UTC(),
				Sequence: client.eventCount.Add(1),
			}
			client.recordState(change)
			select {
			case client.eventInput <- change:
			case <-client.done:
				return
			}
		}
	}
}

func (client *client) recordState(state stateChange) {
	client.stateMutex.Lock()
	if client.latestStates == nil {
		client.latestStates = make(map[string]stateChange)
	}
	client.latestStates[state.State.State] = state
	close(client.stateSignal)
	client.stateSignal = make(chan struct{})
	client.stateMutex.Unlock()
}

func (client *client) relayEvents() {
	var queue []stateChange
	for {
		var output chan stateChange
		var next stateChange
		if len(queue) > 0 {
			output = client.events
			next = queue[0]
		}
		select {
		case event := <-client.eventInput:
			queue = append(queue, event)
		case output <- next:
			queue = queue[1:]
		case <-client.done:
			return
		}
	}
}

func (client *client) deliverResult(result resultMessage) {
	client.mutex.Lock()
	response := client.pending[result.ID]
	delete(client.pending, result.ID)
	client.mutex.Unlock()
	if response != nil {
		response <- result
	}
}

func (client *client) removePending(id int64) {
	client.mutex.Lock()
	delete(client.pending, id)
	client.mutex.Unlock()
}

func (client *client) stop(err error) {
	client.stopOnce.Do(func() {
		client.mutex.Lock()
		client.err = err
		client.mutex.Unlock()
		close(client.done)
	})
}
