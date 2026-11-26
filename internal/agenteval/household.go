package agenteval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	"github.com/mholtzscher/hearth/internal/app/simulator"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

const (
	readyTimeout  = 60 * time.Second
	probeTimeout  = 2 * time.Second
	pinTimeout    = 30 * time.Second
	stopTimeout   = 20 * time.Second
	readTimeout   = 10 * time.Second
	pollInterval  = 100 * time.Millisecond
	brokerTimeout = 10 * time.Second
)

// HouseholdOptions configures one isolated household.
type HouseholdOptions struct {
	// SimulatorConfig is the scripted simulator YAML path.
	SimulatorConfig string
	// Agent supplies non-secret model settings. Its ChatModel is replaced by Model.
	Agent hearthd.AgentConfig
	// Model is the chat model Core's Agent uses, normally a Recorder.
	Model model.ToolCallingChatModel
	// Dir holds the trial's database, broker store, and logs. It must exist.
	Dir string
}

// Household is a running Core, broker, and scripted simulator.
type Household struct {
	CoreURL    string
	ControlURL string
	Inventory  Inventory

	client *http.Client
	broker *natsserver.Server
	// processes are in start order.
	processes []process
	errMu     sync.Mutex
	errs      []error
	logs      *os.File
}

// process is Core or the simulator, running on its own goroutine.
type process struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Inventory maps simulator names to the canonical IDs Core assigned.
type Inventory struct {
	Entities    map[string]string `json:"entities"`    // adapter/device/key → Entity ID
	Devices     map[string]string `json:"devices"`     // adapter/device → Device ID
	Dashboards  map[string]Saved  `json:"dashboards"`  // seeded name → saved dashboard
	Automations map[string]Saved  `json:"automations"` // seeded name → saved Automation
	Labels      map[string]string `json:"labels"`      // lower-cased seeded name → Label ID
}

// Saved identifies a seeded record.
type Saved struct {
	ID       string          `json:"id"`
	Revision int64           `json:"revision"`
	Body     json.RawMessage `json:"body,omitempty"`
}

// StartHousehold starts the broker, Core, and simulator and discovers the inventory.
func StartHousehold(ctx context.Context, options HouseholdOptions) (*Household, error) {
	simulatorConfig, err := platformconfig.LoadYAML[simulator.Config](options.SimulatorConfig, true)
	if err != nil {
		return nil, fmt.Errorf("simulator config: %w", err)
	}
	logs, err := os.Create(filepath.Join(options.Dir, "household.log"))
	if err != nil {
		return nil, err
	}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	broker, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: filepath.Join(options.Dir, "nats"), NoSigs: true,
	})
	if err != nil {
		_ = logs.Close()
		return nil, fmt.Errorf("broker: %w", err)
	}
	go broker.Start()
	if !broker.ReadyForConnections(brokerTimeout) {
		broker.Shutdown()
		_ = logs.Close()
		return nil, errors.New("broker did not become ready")
	}
	household := &Household{
		client: &http.Client{},
		broker: broker, logs: logs,
		Inventory: Inventory{
			Entities: map[string]string{}, Devices: map[string]string{},
			Dashboards: map[string]Saved{}, Automations: map[string]Saved{},
			Labels: map[string]string{},
		},
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.Join(err, household.Stop())
	}
	agentConfig := options.Agent
	agentConfig.ChatModel = options.Model
	coreConfig := hearthd.NormalizeConfig(hearthd.Config{
		HouseholdTimezone: "UTC", HouseholdUnitSystem: "metric",
		HTTPAddr: listener.Addr().String(), NATSURL: broker.ClientURL(),
		SQLitePath: filepath.Join(options.Dir, "hearthd.db"), Agent: agentConfig,
	})
	household.CoreURL = "http://" + listener.Addr().String()
	household.run(ctx, "core", func(ctx context.Context) error {
		return hearthd.RunOnListener(ctx, coreConfig, logger.With(slog.String("process", "core")), listener)
	})
	if err = household.waitOK(ctx, household.CoreURL+"/readyz"); err != nil {
		return nil, errors.Join(fmt.Errorf("core readiness: %w", err), household.Stop())
	}
	controlAddr, err := freeLoopbackAddr(ctx)
	if err != nil {
		return nil, errors.Join(err, household.Stop())
	}
	simulatorConfig.NATSURL = broker.ClientURL()
	simulatorConfig.ControlAddr = controlAddr
	household.ControlURL = "http://" + controlAddr
	household.run(ctx, "simulator", func(ctx context.Context) error {
		return simulator.Run(ctx, simulatorConfig, logger.With(slog.String("process", "simulator")))
	})
	if err = household.waitOK(ctx, household.ControlURL+"/v1/sim/entities"); err != nil {
		return nil, errors.Join(fmt.Errorf("simulator readiness: %w", err), household.Stop())
	}
	if err = household.discover(ctx); err != nil {
		return nil, errors.Join(err, household.Stop())
	}
	return household, nil
}

func (household *Household) run(ctx context.Context, name string, fn func(context.Context) error) {
	ctx, cancel := context.WithCancel(ctx)
	running := process{cancel: cancel, done: make(chan struct{})}
	household.processes = append(household.processes, running)
	go func() {
		defer close(running.done)
		if err := fn(ctx); err != nil && !errors.Is(err, context.Canceled) {
			household.errMu.Lock()
			household.errs = append(household.errs, fmt.Errorf("%s: %w", name, err))
			household.errMu.Unlock()
		}
	}()
}

// Stop shuts everything down and reports process failures observed while
// running. Each step below avoids a 5-second shutdown timeout:
//   - Idle client connections close first. An HTTP server's graceful
//     shutdown waits that long for a connection that never sent a request.
//   - Processes stop in reverse start order, so the simulator releases its
//     Adapter sessions while Core can still answer.
func (household *Household) Stop() error {
	household.client.CloseIdleConnections()
	timeout := time.NewTimer(stopTimeout)
	defer timeout.Stop()
	for _, running := range slices.Backward(household.processes) {
		running.cancel()
		select {
		case <-running.done:
		case <-timeout.C:
			for _, remaining := range household.processes {
				remaining.cancel()
			}
			// Core or the simulator may still use the broker and log file, so
			// leave them open; the worker process exits soon after.
			household.errMu.Lock()
			defer household.errMu.Unlock()
			return errors.Join(errors.Join(household.errs...), errors.New("household did not stop in time"))
		}
	}
	household.broker.Shutdown()
	household.broker.WaitForShutdown()
	_ = household.logs.Close()
	household.errMu.Lock()
	defer household.errMu.Unlock()
	return errors.Join(household.errs...)
}

func (household *Household) waitOK(ctx context.Context, url string) error {
	deadline := time.Now().Add(readyTimeout)
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		response, err := (&http.Client{Timeout: probeTimeout}).Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		household.errMu.Lock()
		failed := len(household.errs) > 0
		household.errMu.Unlock()
		if failed {
			household.errMu.Lock()
			defer household.errMu.Unlock()
			return errors.Join(household.errs...)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s not ready within %s", url, readyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func (household *Household) discover(ctx context.Context) error {
	var snapshots []struct {
		EntityID   string `json:"entity_id"`
		AdapterID  string `json:"adapter_id"`
		BindingKey string `json:"binding_key"`
		Key        string `json:"key"`
	}
	if err := household.getJSON(ctx, household.ControlURL+"/v1/sim/entities", &snapshots); err != nil {
		return fmt.Errorf("simulator inventory: %w", err)
	}
	for _, snapshot := range snapshots {
		household.Inventory.Entities[snapshot.AdapterID+"/"+snapshot.BindingKey+"/"+snapshot.Key] = snapshot.EntityID
		device := snapshot.AdapterID + "/" + snapshot.BindingKey
		if _, known := household.Inventory.Devices[device]; known {
			continue
		}
		var entity struct {
			DeviceID string `json:"device_id"`
		}
		if err := household.waitEntity(ctx, snapshot.EntityID, &entity); err != nil {
			return err
		}
		household.Inventory.Devices[device] = entity.DeviceID
	}
	return nil
}

// waitEntity reads an Entity, retrying while Core finishes registration.
func (household *Household) waitEntity(ctx context.Context, id string, into any) error {
	deadline := time.Now().Add(readyTimeout)
	for {
		err := household.getJSON(ctx, household.CoreURL+"/v1/entities/"+id, into)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// Pin pauses the Entity and publishes value, waiting until Core records it.
func (household *Household) Pin(ctx context.Context, pin Pin) error {
	id, ok := household.Inventory.Entities[pin.Entity.InventoryKey()]
	if !ok {
		return fmt.Errorf("pin: unknown Entity %s", pin.Entity.InventoryKey())
	}
	if err := household.postJSON(ctx, household.ControlURL+"/v1/sim/entities/"+id+"/pause", nil, nil); err != nil {
		return fmt.Errorf("pin pause: %w", err)
	}
	var published struct {
		ObservationID string `json:"observation_id"`
	}
	body := map[string]any{"value": pin.Value}
	if err := household.postJSON(
		ctx,
		household.ControlURL+"/v1/sim/entities/"+id+"/publish",
		body,
		&published,
	); err != nil {
		return fmt.Errorf("pin publish: %w", err)
	}
	deadline := time.Now().Add(pinTimeout)
	for {
		var entity struct {
			State *struct {
				ObservationID string `json:"observation_id"`
			} `json:"state"`
		}
		if err := household.getJSON(ctx, household.CoreURL+"/v1/entities/"+id, &entity); err == nil &&
			entity.State != nil && entity.State.ObservationID == published.ObservationID {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pin: Core did not record %s within %s", pin.Entity.InventoryKey(), pinTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func (household *Household) getJSON(ctx context.Context, url string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return household.do(request, into)
}

func (household *Household) postJSON(ctx context.Context, url string, body, into any) error {
	return household.sendJSON(ctx, http.MethodPost, "application/json", url, body, into)
}

// patchJSON sends a JSON Merge Patch.
func (household *Household) patchJSON(ctx context.Context, url string, body, into any) error {
	return household.sendJSON(ctx, http.MethodPatch, "application/merge-patch+json", url, body, into)
}

func (household *Household) sendJSON(ctx context.Context, method, contentType, url string, body, into any) error {
	var reader io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	return household.do(request, into)
}

// do sends one request and decodes a 2xx JSON body. Error bodies are not
// echoed: they can contain household data.
func (household *Household) do(request *http.Request, into any) error {
	response, err := household.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return &StatusError{Method: request.Method, Path: request.URL.Path, Status: response.StatusCode}
	}
	if into == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

// StatusError is a non-2xx response from Core or the simulator.
type StatusError struct {
	Method, Path string
	Status       int
}

func (err *StatusError) Error() string {
	path := err.Path
	if index := strings.LastIndex(path, "/"); index > 0 && strings.Contains(path[index:], "_") {
		path = path[:index] + "/{id}"
	}
	return fmt.Sprintf("%s %s: HTTP %d", err.Method, path, err.Status)
}

// freeLoopbackAddr reserves and releases a port for the simulator's control
// channel, which only accepts an address.
func freeLoopbackAddr(ctx context.Context) (string, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := listener.Addr().String()
	return addr, listener.Close()
}
