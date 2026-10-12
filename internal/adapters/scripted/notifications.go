package scripted

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const NotificationCaptureLimit = 1000

var (
	ErrUnknownNotificationDestination = errors.New("unknown scripted notification destination")
	ErrUnknownNotificationAttempt     = errors.New("unknown scripted notification attempt")
)

type NotificationSpec struct {
	Key        string `yaml:"key"`
	Name       string `yaml:"name"`
	Outcome    string `yaml:"outcome"`
	ReasonCode string `yaml:"reason_code"`
	Hold       bool   `yaml:"hold"`
}

type NotificationBehavior struct {
	Outcome    string `json:"outcome"`
	ReasonCode string `json:"reason_code,omitempty"`
	Hold       bool   `json:"hold"`
}

// Validate requires a complete behavior; only configuration defaults Outcome.
// Evidence policy belongs to notifications, not scripted Entity health rules.
func (behavior NotificationBehavior) Validate() error {
	return notifications.ValidateEvidence(notifications.Evidence{
		Outcome: notifications.Outcome(behavior.Outcome), ReasonCode: behavior.ReasonCode,
	})
}

func (spec NotificationSpec) behavior() NotificationBehavior {
	outcome := spec.Outcome
	if outcome == "" {
		outcome = string(notifications.Accepted)
	}
	return NotificationBehavior{Outcome: outcome, ReasonCode: spec.ReasonCode, Hold: spec.Hold}
}

// ValidateNotifications is the static config boundary and permits an empty
// collection for Device-only Adapters. It shares registration and evidence rules.
func ValidateNotifications(specs []NotificationSpec) error {
	seen := make(map[string]bool, len(specs))
	for index, spec := range specs {
		if _, err := notifications.PrepareDestinationDescriptor(spec.Key, spec.Name); err != nil {
			return fmt.Errorf("notifications[%d]: %w", index, err)
		}
		if seen[spec.Key] {
			return fmt.Errorf("notifications[%d]: duplicate key %q", index, spec.Key)
		}
		seen[spec.Key] = true
		if err := spec.behavior().Validate(); err != nil {
			return fmt.Errorf("notifications[%d]: invalid outcome/reason_code: %w", index, err)
		}
	}
	return nil
}

type NotificationDestinationInfo struct {
	AdapterID     string               `json:"adapter_id"`
	Key           string               `json:"key"`
	DestinationID string               `json:"destination_id"`
	Behavior      NotificationBehavior `json:"behavior"`
}

type NotificationCapture struct {
	AdapterID     string                      `json:"adapter_id"`
	Sequence      uint64                      `json:"sequence"`
	AttemptID     string                      `json:"attempt_id"`
	DestinationID string                      `json:"destination_id"`
	LocalKey      string                      `json:"local_key"`
	Message       adapter.NotificationMessage `json:"message"`
	ReceivedAt    time.Time                   `json:"received_at"`
	Deadline      time.Time                   `json:"deadline"`
	Behavior      NotificationBehavior        `json:"behavior"`
	FinishedAt    *time.Time                  `json:"finished_at"`
}

// NotificationCaptureMetadata describes the whole Adapter retention window,
// not just a page. Oldest alone cannot expose holes behind a pending request.
// LatestSequence includes overflow admissions; DroppedCount includes both
// evictions and overflow requests which could not be captured. Truncated remains
// true after any loss until restart; captures are not durable send evidence.
type NotificationCaptureMetadata struct {
	AdapterID              string `json:"adapter_id"`
	OldestSequence         uint64 `json:"oldest_sequence"`
	LatestSequence         uint64 `json:"latest_sequence"`
	HighestEvictedSequence uint64 `json:"highest_evicted_sequence"`
	HighestDroppedSequence uint64 `json:"highest_dropped_sequence"`
	DroppedCount           uint64 `json:"dropped_count"`
	Truncated              bool   `json:"truncated"`
}

type NotificationCapturePage struct {
	Items    []NotificationCapture       `json:"items"`
	Metadata NotificationCaptureMetadata `json:"metadata"`
	HasMore  bool                        `json:"has_more"`
}

type notificationDestination struct {
	spec NotificationSpec
	info NotificationDestinationInfo
}

type notificationEntry struct {
	capture  NotificationCapture
	release  chan struct{}
	released bool
}

// NotificationRuntime owns one Adapter's scripted destinations and bounded
// captures. It does not own SDK serving, registration I/O, or app lifecycle.
type NotificationRuntime struct {
	mutex        sync.Mutex
	adapterID    string
	destinations []*notificationDestination
	byKey        map[string]*notificationDestination
	byID         map[string]*notificationDestination
	entries      []*notificationEntry
	byAttempt    map[string]*notificationEntry
	metadata     NotificationCaptureMetadata
}

func NewNotificationRuntime(adapterID string, specs []NotificationSpec) (*NotificationRuntime, error) {
	if err := platformconfig.ValidateSlug("adapter_id", adapterID); err != nil {
		return nil, err
	}
	if err := ValidateNotifications(specs); err != nil {
		return nil, err
	}
	runtime := &NotificationRuntime{
		adapterID: adapterID, byKey: make(map[string]*notificationDestination),
		byID: make(map[string]*notificationDestination), byAttempt: make(map[string]*notificationEntry),
		metadata: NotificationCaptureMetadata{AdapterID: adapterID},
	}
	for _, spec := range specs {
		name, _ := notifications.PrepareDestinationDescriptor(spec.Key, spec.Name)
		spec.Name = name
		destination := &notificationDestination{spec: spec, info: NotificationDestinationInfo{
			AdapterID: adapterID, Key: spec.Key, Behavior: spec.behavior(),
		}}
		runtime.destinations = append(runtime.destinations, destination)
		runtime.byKey[spec.Key] = destination
	}
	return runtime, nil
}

// Registrations returns normalized specs in config order for app-owned SDK registration.
func (runtime *NotificationRuntime) Registrations() []NotificationSpec {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	result := make([]NotificationSpec, 0, len(runtime.destinations))
	for _, destination := range runtime.destinations {
		result = append(result, destination.spec)
	}
	return result
}

// Attach retains Core's canonical destination identity after registration.
func (runtime *NotificationRuntime) Attach(key, destinationID string) error {
	if err := notifications.ValidateID(destinationID, "ndst_"); err != nil {
		return err
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	destination := runtime.byKey[key]
	if destination == nil {
		return ErrUnknownNotificationDestination
	}
	if existing := runtime.byID[destinationID]; existing != nil && existing != destination {
		return fmt.Errorf("registration reused destination ID %q", destinationID)
	}
	if destination.info.DestinationID != "" && destination.info.DestinationID != destinationID {
		return fmt.Errorf("registration changed destination ID for %q", key)
	}
	destination.info.DestinationID = destinationID
	runtime.byID[destinationID] = destination
	return nil
}

func (runtime *NotificationRuntime) Destinations() []NotificationDestinationInfo {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	result := make([]NotificationDestinationInfo, 0, len(runtime.destinations))
	for _, destination := range runtime.destinations {
		if destination.info.DestinationID != "" {
			result = append(result, destination.info)
		}
	}
	return result
}

func (runtime *NotificationRuntime) SetBehavior(destinationID string, behavior NotificationBehavior) error {
	if err := behavior.Validate(); err != nil {
		return err
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	destination := runtime.byID[destinationID]
	if destination == nil {
		return ErrUnknownNotificationDestination
	}
	destination.info.Behavior = behavior
	return nil
}

func (runtime *NotificationRuntime) Release(attemptID string) error {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	entry := runtime.byAttempt[attemptID]
	if entry == nil {
		return ErrUnknownNotificationAttempt
	}
	if entry.capture.FinishedAt == nil && !entry.released {
		close(entry.release)
		entry.released = true
	}
	return nil
}

// Requests returns one Adapter's exclusive sequence page in receive order.
// Callers aggregate Adapters themselves; sequences are never global cursors.
func (runtime *NotificationRuntime) Requests(afterSequence uint64, limit int) (NotificationCapturePage, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > NotificationCaptureLimit {
		return NotificationCapturePage{}, fmt.Errorf(
			"notification capture limit must be 1-%d",
			NotificationCaptureLimit,
		)
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	page := NotificationCapturePage{Items: make([]NotificationCapture, 0), Metadata: runtime.metadata}
	if len(runtime.entries) > 0 {
		page.Metadata.OldestSequence = runtime.entries[0].capture.Sequence
	}
	for _, entry := range runtime.entries {
		if entry.capture.Sequence <= afterSequence {
			continue
		}
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		capture := entry.capture
		if capture.FinishedAt != nil {
			finished := *capture.FinishedAt
			capture.FinishedAt = &finished
		}
		page.Items = append(page.Items, capture)
	}
	return page, nil
}

func (runtime *NotificationRuntime) Handler() adapter.NotificationHandler {
	return func(ctx context.Context, request adapter.NotificationRequest) adapter.NotificationOutcome {
		entry, outcome := runtime.admit(request)
		if entry == nil {
			return outcome
		}
		if entry.capture.Behavior.Hold {
			select {
			case <-entry.release:
			case <-ctx.Done():
			}
		}
		// Cancellation takes precedence over a simultaneous release: no simulated
		// provider work occurred. The SDK owns the actual deadline context.
		if ctx.Err() != nil {
			outcome = adapter.NotificationOutcome{
				Outcome:    string(notifications.NotSent),
				ReasonCode: "destination_unavailable",
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				outcome.ReasonCode = "deadline_expired"
			}
		}
		runtime.mutex.Lock()
		finished := time.Now().UTC()
		entry.capture.FinishedAt = &finished
		runtime.mutex.Unlock()
		return outcome
	}
}

// admit atomically reserves capacity, snapshots behavior, and records the exact
// SDK request. Only finished entries can be evicted, oldest receive order first.
func (runtime *NotificationRuntime) admit(
	request adapter.NotificationRequest,
) (*notificationEntry, adapter.NotificationOutcome) {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	destination := runtime.byKey[request.LocalKey]
	if destination == nil {
		return nil, adapter.NotificationOutcome{Outcome: "rejected", ReasonCode: "destination_unknown"}
	}
	runtime.metadata.LatestSequence++
	sequence := runtime.metadata.LatestSequence
	// SDK eligibility precedes the registration reply. Until Attach supplies
	// Core's identity, do not simulate delivery or capture an unidentified send,
	// but account for the actual receipt so absence cannot imply no dispatch.
	if destination.info.DestinationID == "" || runtime.byAttempt[request.AttemptID] != nil || !runtime.makeRoom() {
		runtime.recordDrop(sequence)
		return nil, adapter.NotificationOutcome{Outcome: string(notifications.NotSent), ReasonCode: "adapter_busy"}
	}
	behavior := destination.info.Behavior
	entry := &notificationEntry{release: make(chan struct{}), capture: NotificationCapture{
		AdapterID: runtime.adapterID, Sequence: sequence, AttemptID: request.AttemptID,
		DestinationID: destination.info.DestinationID, LocalKey: request.LocalKey,
		Message: request.Message, ReceivedAt: time.Now().UTC(), Deadline: request.Deadline, Behavior: behavior,
	}}
	runtime.entries = append(runtime.entries, entry)
	runtime.byAttempt[request.AttemptID] = entry
	return entry, adapter.NotificationOutcome{Outcome: behavior.Outcome, ReasonCode: behavior.ReasonCode}
}

// makeRoom and recordDrop require runtime.mutex.
func (runtime *NotificationRuntime) makeRoom() bool {
	if len(runtime.entries) < NotificationCaptureLimit {
		return true
	}
	for index, entry := range runtime.entries {
		if entry.capture.FinishedAt == nil {
			continue
		}
		delete(runtime.byAttempt, entry.capture.AttemptID)
		runtime.metadata.HighestEvictedSequence = max(runtime.metadata.HighestEvictedSequence, entry.capture.Sequence)
		runtime.recordDrop(entry.capture.Sequence)
		copy(runtime.entries[index:], runtime.entries[index+1:])
		runtime.entries[len(runtime.entries)-1] = nil
		runtime.entries = runtime.entries[:len(runtime.entries)-1]
		return true
	}
	return false
}

func (runtime *NotificationRuntime) recordDrop(sequence uint64) {
	runtime.metadata.HighestDroppedSequence = max(runtime.metadata.HighestDroppedSequence, sequence)
	runtime.metadata.DroppedCount++
	runtime.metadata.Truncated = true
}
