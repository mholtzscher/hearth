// Package homekit translates between Apple Home and Hearth's public interfaces.
package homekit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	inventoryPageSize    = "200"
	inventoryTimeout     = 10 * time.Second
	maxCoreResponseBytes = 4 << 20
)

// Device is the subset of a Core Device the bridge uses.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Entity contains the canonical State and metadata needed by the bridge.
type Entity struct {
	ID, DeviceID, Name, Type string
	Support                  json.RawMessage
	Enabled                  bool
	Availability             string
	State                    *EntityState
}

// EntityState is a canonical value with its Observation evidence.
type EntityState struct {
	Value         json.RawMessage `json:"value"`
	ObservationID string          `json:"observation_id"`
	ObservedAt    time.Time       `json:"observed_at"`
}

// Inventory is a completed pass through the Core collections, not a snapshot.
type Inventory struct {
	Devices  map[string]Device
	Entities map[string]Entity
	ReadAt   time.Time
}

// CommandError describes a Core HTTP rejection. Receipt fields are empty when
// absent or not recognized. Neither absence nor a timeout proves non-execution.
type CommandError struct {
	HTTPStatus int
	Code       string
	CommandID  string
}

func (err *CommandError) Error() string {
	return "Core Command returned HTTP " + strconv.Itoa(err.HTTPStatus)
}

// CoreClient reads and controls Devices through Core HTTP. It never replays a Command.
type CoreClient struct {
	baseURL *url.URL
	http    *http.Client
}

// NewCoreClient creates a client for one configured Core without following redirects.
func NewCoreClient(baseURL string) (*CoreClient, error) {
	base, err := parseCoreURL(baseURL)
	if err != nil {
		return nil, err
	}
	return &CoreClient{baseURL: base, http: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// ValidateCoreURL checks a Core base URL without exposing configuration values.
func ValidateCoreURL(value string) error {
	_, err := parseCoreURL(value)
	return err
}

func parseCoreURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") || u.RawQuery != "" || u.ForceQuery ||
		strings.Contains(value, "#") || !validCorePort(u) {
		return nil, errors.New(
			"core_url must be an absolute HTTP or HTTPS base URL without credentials, a path prefix, query, or fragment",
		)
	}
	return u, nil
}

func validCorePort(u *url.URL) bool {
	const portBits = 16
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, portBits)
		return err == nil && n > 0
	}
	return !strings.HasSuffix(u.Host, ":")
}

// ReadInventory follows both collections' cursors using canonical Entity units.
func (client *CoreClient) ReadInventory(ctx context.Context) (Inventory, error) {
	ctx, cancel := context.WithTimeout(ctx, inventoryTimeout)
	defer cancel()
	devices, err := readCollection(ctx, client, "devices", nil, inventoryDevice)
	if err != nil {
		return Inventory{}, fmt.Errorf("read Core Devices: %w", err)
	}
	entities, err := readCollection(ctx, client, "entities", url.Values{"units": {"canonical"}}, inventoryEntity)
	if err != nil {
		return Inventory{}, fmt.Errorf("read Core Entities: %w", err)
	}
	return Inventory{Devices: devices, Entities: entities, ReadAt: time.Now().UTC()}, nil
}

// ExecuteCommand posts once and accepts only a confirmed Core outcome. The
// caller chooses a deadline that includes the Operation's Command deadline.
func (client *CoreClient) ExecuteCommand(ctx context.Context, entityID, operation string, parameters any) error {
	if !canonicalID(entityID, "ent_") || operation == "" {
		return errors.New("expected a canonical Entity ID and Command operation")
	}
	body, err := json.Marshal(commandRequest{Operation: operation, Parameters: parameters})
	if err != nil {
		return fmt.Errorf("encode Core Command: %w", err)
	}
	path := client.baseURL.JoinPath("v1", "entities", entityID, "commands")
	status, payload, err := client.request(ctx, http.MethodPost, path, body)
	if err != nil {
		return fmt.Errorf("send Core Command: %w", err)
	}
	if status != http.StatusOK {
		return commandProblem(status, payload)
	}
	var response commandResponse
	if json.Unmarshal(payload, &response) != nil || !response.valid() {
		return errors.New("expected a confirmed outcome from Core")
	}
	return nil
}

func (client *CoreClient) request(ctx context.Context, method string, u *url.URL, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxCoreResponseBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(payload) > maxCoreResponseBytes {
		return 0, nil, errors.New("received an oversized Core HTTP response")
	}
	return resp.StatusCode, payload, nil
}

type collection[W any] struct {
	Items      []W    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func readCollection[W, T any](
	ctx context.Context, client *CoreClient, resource string, filters url.Values, convert func(W) (string, T, error),
) (map[string]T, error) {
	q := url.Values{"limit": {inventoryPageSize}}
	maps.Copy(q, filters)
	u := client.baseURL.JoinPath("v1", resource)
	result := make(map[string]T)
	seen := make(map[string]bool)
	for {
		u.RawQuery = q.Encode()
		page, err := readPage[W](ctx, client, u)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			key, value, convertErr := convert(item)
			if convertErr != nil {
				return nil, convertErr
			}
			result[key] = value
		}
		if page.NextCursor == "" {
			return result, nil
		}
		if seen[page.NextCursor] {
			return nil, errors.New("received a repeated Core inventory cursor")
		}
		seen[page.NextCursor] = true
		q.Set("cursor", page.NextCursor)
	}
}

func readPage[W any](ctx context.Context, client *CoreClient, u *url.URL) (collection[W], error) {
	var page collection[W]
	status, payload, err := client.request(ctx, http.MethodGet, u, nil)
	if err != nil {
		return page, err
	}
	if status != http.StatusOK {
		return page, fmt.Errorf("received HTTP %d reading Core collection", status)
	}
	if json.Unmarshal(payload, &page) != nil || page.Items == nil {
		return page, errors.New("expected a Core inventory collection")
	}
	return page, nil
}

func inventoryDevice(device Device) (string, Device, error) {
	if !canonicalID(device.ID, "dev_") || device.Name == "" || device.Kind == "" {
		return "", Device{}, errors.New("received an invalid Core Device")
	}
	return device.ID, device, nil
}

type entityResponse struct {
	ID           string          `json:"id"`
	DeviceID     string          `json:"device_id"`
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Support      json.RawMessage `json:"support"`
	Enabled      *bool           `json:"enabled"`
	Availability struct {
		Status string `json:"status"`
	} `json:"availability"`
	State *EntityState `json:"state"`
}

func inventoryEntity(w entityResponse) (string, Entity, error) {
	if !canonicalID(w.ID, "ent_") || !canonicalID(w.DeviceID, "dev_") || w.Name == "" ||
		w.Type == "" || len(w.Support) == 0 || w.Support[0] != '{' || w.Enabled == nil ||
		!validAvailability(w.Availability.Status) || !validEntityState(w.State) {
		return "", Entity{}, errors.New("received an invalid Core Entity")
	}
	return w.ID, Entity{
		ID: w.ID, DeviceID: w.DeviceID, Name: w.Name, Type: w.Type, Support: w.Support,
		Enabled: *w.Enabled, Availability: w.Availability.Status, State: w.State,
	}, nil
}

func validAvailability(value string) bool {
	return value == "available" || value == "unavailable" || value == "unknown"
}

func validEntityState(state *EntityState) bool {
	return state == nil || (canonicalID(state.ObservationID, "obs_") && !state.ObservedAt.IsZero() &&
		len(state.Value) > 0 && !bytes.Equal(state.Value, []byte("null")))
}

func canonicalID(value, prefix string) bool {
	const uuidV7 = 7
	encoded, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return false
	}
	id, err := uuid.Parse(encoded)
	return err == nil && id.Version() == uuidV7 && id.Variant() == uuid.RFC4122 && id.String() == encoded
}

type commandRequest struct {
	Operation  string `json:"operation"`
	Parameters any    `json:"parameters"`
}

type commandResponse struct {
	CommandID     string          `json:"command_id"`
	Status        string          `json:"status"`
	ObservationID string          `json:"observation_id"`
	Value         json.RawMessage `json:"value"`
}

func (result commandResponse) valid() bool {
	if !canonicalID(result.CommandID, "cmd_") {
		return false
	}
	switch result.Status {
	case "dispatched":
		return result.ObservationID == "" && len(result.Value) == 0
	case "satisfied":
		return canonicalID(result.ObservationID, "obs_") && len(result.Value) > 0 &&
			!bytes.Equal(result.Value, []byte("null"))
	default:
		return false
	}
}

func commandProblem(status int, payload []byte) *CommandError {
	failure := &CommandError{HTTPStatus: status}
	var problem struct {
		Code      string `json:"code"`
		CommandID string `json:"command_id"`
	}
	if status == http.StatusConflict && json.Unmarshal(payload, &problem) == nil &&
		problem.Code == "entity_disabled" && canonicalID(problem.CommandID, "cmd_") {
		failure.Code, failure.CommandID = problem.Code, problem.CommandID
	}
	return failure
}
