package homekit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type storeFile struct {
	Version   int                          `json:"version"`
	Setup     SetupIdentity                `json:"setup"`
	PairEpoch uint64                       `json:"pair_epoch,omitempty"`
	NextAID   uint64                       `json:"next_aid"`
	Exposures map[string]Exposure          `json:"exposures"`
	Retired   map[string]RetiredAllocation `json:"retired"`
}

type RetiredAllocation struct {
	AID   uint64            `json:"aid"`
	Slots map[string]uint16 `json:"slots"`
}

// errStoreDurabilityUncertain means rename succeeded but syncing the directory
// failed. The new file is visible, but may not survive a crash.
var errStoreDurabilityUncertain = errors.New("bridge store durability uncertain")

const firstAccessoryAID = 2
const accessoryLimit = 149

type store struct {
	mu           sync.Mutex
	dir          string
	data         storeFile
	beforeRename func() error // test-only fault injection
	syncDir      func(*os.File) error
}

func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create bridge store directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat bridge store directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("bridge store directory must be private")
	}
	s := &store{dir: dir}
	path := filepath.Join(dir, "bridge.json")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		setup, setupErr := newSetupIdentity()
		if setupErr != nil {
			return nil, setupErr
		}
		data := storeFile{
			Version:   1,
			Setup:     setup,
			NextAID:   firstAccessoryAID,
			Exposures: map[string]Exposure{},
			Retired:   map[string]RetiredAllocation{},
		}
		if writeErr := s.write(data); writeErr != nil {
			return nil, writeErr
		}
		s.data = data
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open bridge store: %w", err)
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat bridge store: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("bridge store file must be private and regular")
	}
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&s.data); err != nil {
		return nil, fmt.Errorf("decode bridge store: %w", err)
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid trailing bridge store data")
	}
	if err = s.data.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (f storeFile) validate() error {
	if f.Version != 1 || !f.Setup.valid() || f.NextAID < 2 || f.Exposures == nil || f.Retired == nil ||
		len(f.Exposures) > accessoryLimit {
		return errors.New("invalid bridge store")
	}
	seen := make(map[uint64]bool)
	for id, e := range f.Exposures {
		if id != e.DeviceID || e.Kind == "" || e.ExposedAt.IsZero() {
			return errors.New("invalid bridge exposure")
		}
		if err := validateAllocation(id, e.AID, e.Slots, f.NextAID, seen); err != nil {
			return err
		}
		for _, assignment := range e.Entities {
			key, err := slotKey(assignment)
			if err != nil || e.Slots[key] == 0 {
				return errors.New("invalid bridge exposure assignment")
			}
		}
	}
	for id, allocation := range f.Retired {
		if _, ok := f.Exposures[id]; ok {
			return errors.New("bridge allocation both exposed and retired")
		}
		if err := validateAllocation(id, allocation.AID, allocation.Slots, f.NextAID, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateAllocation(
	device string,
	aid uint64,
	slots map[string]uint16,
	nextAID uint64,
	seen map[uint64]bool,
) error {
	if device == "" || aid < firstAccessoryAID || aid >= nextAID || seen[aid] || slots == nil {
		return errors.New("invalid bridge allocation")
	}
	seen[aid] = true
	used := make(map[uint16]bool)
	for key, slot := range slots {
		role, entity, ok := strings.Cut(key, "/")
		if !ok || role == "" || entity == "" || slot == 0 || used[slot] {
			return errors.New("invalid bridge slot")
		}
		used[slot] = true
	}
	return nil
}

func slotKey(a RoleAssignment) (string, error) {
	if a.Role == "" || strings.Contains(string(a.Role), "/") || a.EntityID == "" {
		return "", errors.New("invalid role assignment")
	}
	return string(a.Role) + "/" + a.EntityID, nil
}

// snapshot returns independent copies; callers cannot mutate the store.
func (s *store) snapshot() storeFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStoreFile(s.data)
}

func (s *store) hasAssignment(exposure Exposure, entityID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.data.Exposures[exposure.DeviceID]
	if !ok || current.Kind != exposure.Kind || current.AID != exposure.AID {
		return false
	}
	for _, selected := range exposure.Entities {
		if selected.EntityID != entityID {
			continue
		}
		return slices.Contains(current.Entities, selected)
	}
	return false
}

func cloneStoreFile(f storeFile) storeFile {
	result := f
	result.Exposures = make(map[string]Exposure, len(f.Exposures))
	for id, e := range f.Exposures {
		e.Entities = append([]RoleAssignment(nil), e.Entities...)
		e.Slots = cloneSlots(e.Slots)
		result.Exposures[id] = e
	}
	result.Retired = make(map[string]RetiredAllocation, len(f.Retired))
	for id, allocation := range f.Retired {
		allocation.Slots = cloneSlots(allocation.Slots)
		result.Retired[id] = allocation
	}
	return result
}

func cloneSlots(slots map[string]uint16) map[string]uint16 {
	result := make(map[string]uint16, len(slots))
	maps.Copy(result, slots)
	return result
}

// expose retains all historical slots, including assignments no longer active.
// Validation of kind and roles against inventory belongs to the planner.
func (s *store) expose(exposure Exposure) (Exposure, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if exposure.DeviceID == "" || exposure.Kind == "" {
		return Exposure{}, errors.New("invalid exposure")
	}
	next := cloneStoreFile(s.data)
	old, exists := next.Exposures[exposure.DeviceID]
	if !exists && len(next.Exposures) >= accessoryLimit {
		return Exposure{}, &PlanError{Reason: "capacity_reached"}
	}
	if exists {
		exposure.AID, exposure.Slots, exposure.ExposedAt = old.AID, old.Slots, old.ExposedAt
	} else if retired, ok := next.Retired[exposure.DeviceID]; ok {
		exposure.AID, exposure.Slots = retired.AID, retired.Slots
		delete(next.Retired, exposure.DeviceID)
	} else {
		if next.NextAID == math.MaxUint64 {
			return Exposure{}, errors.New("bridge AID space exhausted")
		}
		exposure.AID, exposure.Slots = next.NextAID, make(map[string]uint16)
		next.NextAID++
	}
	if !exists {
		exposure.ExposedAt = time.Now().UTC()
	}
	if err := allocateSlots(&exposure); err != nil {
		return Exposure{}, err
	}
	exposure.Entities = append([]RoleAssignment(nil), exposure.Entities...)
	next.Exposures[exposure.DeviceID] = exposure
	if err := s.commit(next); err != nil {
		return Exposure{}, err
	}
	exposure.Slots = cloneSlots(exposure.Slots)
	exposure.Entities = append([]RoleAssignment(nil), exposure.Entities...)
	return exposure, nil
}

func allocateSlots(exposure *Exposure) error {
	var maxSlot uint16
	for _, slot := range exposure.Slots {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	seen := make(map[string]bool)
	for _, assignment := range exposure.Entities {
		key, err := slotKey(assignment)
		if err != nil || seen[key] {
			return errors.New("invalid or duplicate role assignment")
		}
		seen[key] = true
		if exposure.Slots[key] == 0 {
			if maxSlot == math.MaxUint16 {
				return errors.New("bridge slot space exhausted")
			}
			maxSlot++
			exposure.Slots[key] = maxSlot
		}
	}
	return nil
}

func (s *store) unexpose(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneStoreFile(s.data)
	e, ok := next.Exposures[deviceID]
	if !ok {
		return errors.New("exposure not found")
	}
	next.Retired[deviceID] = RetiredAllocation{AID: e.AID, Slots: e.Slots}
	delete(next.Exposures, deviceID)
	return s.commit(next)
}

func (s *store) commit(next storeFile) error {
	if err := next.validate(); err != nil {
		return err
	}
	err := s.write(next)
	if err == nil || errors.Is(err, errStoreDurabilityUncertain) {
		s.data = next
	}
	return err
}

func (s *store) rotatePairing() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.PairEpoch == math.MaxUint64 {
		return errors.New("bridge pairing generations exhausted")
	}
	setup, err := newSetupIdentity()
	if err != nil {
		return err
	}
	next := s.data
	next.Setup = setup
	next.PairEpoch++
	return s.commit(next)
}

func (s *store) write(next storeFile) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bridge store: %w", err)
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(s.dir, ".bridge-*.tmp")
	if err != nil {
		return fmt.Errorf("create bridge store temporary file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err = io.Copy(f, bytes.NewReader(data)); err != nil {
		_ = f.Close()
		return fmt.Errorf("write bridge store: %w", err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync bridge store: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close bridge store: %w", err)
	}
	if s.beforeRename != nil {
		if err = s.beforeRename(); err != nil {
			return fmt.Errorf("before bridge store rename: %w", err)
		}
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("open bridge store directory: %w", err)
	}
	defer dir.Close()
	if err = os.Rename(f.Name(), filepath.Join(s.dir, "bridge.json")); err != nil {
		return fmt.Errorf("rename bridge store: %w", err)
	}
	syncDir := dir.Sync
	if s.syncDir != nil {
		syncDir = func() error { return s.syncDir(dir) }
	}
	if err = syncDir(); err != nil {
		return fmt.Errorf("%w: %w", errStoreDurabilityUncertain, err)
	}
	return nil
}
