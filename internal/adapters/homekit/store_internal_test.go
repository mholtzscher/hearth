package homekit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRetainsAllocationsAcrossRestart(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "private")
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := Exposure{
		DeviceID: "one",
		Kind:     KindSwitch,
		Entities: []RoleAssignment{{Role: "on", EntityID: "a"}, {Role: "on", EntityID: "b"}},
	}
	first, err := s.expose(original)
	if err != nil {
		t.Fatal(err)
	}
	if first.AID != 2 || first.Slots["on/a"] != 1 || first.Slots["on/b"] != 2 {
		t.Fatalf("unexpected allocation: %+v", first)
	}
	original.Entities = []RoleAssignment{{Role: "on", EntityID: "b"}, {Role: "on", EntityID: "c"}}
	updated, err := s.expose(original)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Slots["on/a"] != 1 || updated.Slots["on/b"] != 2 || updated.Slots["on/c"] != 3 {
		t.Fatalf("slots not retained: %+v", updated.Slots)
	}
	if err = s.unexpose("one"); err != nil {
		t.Fatal(err)
	}
	second, err := s.expose(Exposure{DeviceID: "two", Kind: KindSwitch})
	if err != nil {
		t.Fatal(err)
	}
	if second.AID != 3 {
		t.Fatalf("next AID = %d", second.AID)
	}
	setup := s.snapshot().Setup
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.expose(
		Exposure{
			DeviceID: "one",
			Kind:     KindSwitch,
			Entities: []RoleAssignment{{Role: "on", EntityID: "a"}, {Role: "on", EntityID: "d"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.AID != 2 || restored.Slots["on/a"] != 1 || restored.Slots["on/d"] != 4 || s.snapshot().Setup != setup {
		t.Fatalf("restoration failed: %+v", restored)
	}
	restored.Slots["on/a"] = 99
	snapshot := s.snapshot()
	snapshot.Exposures["one"].Slots["on/a"] = 88
	if s.snapshot().Exposures["one"].Slots["on/a"] != 1 {
		t.Fatal("returned maps alias store")
	}
}

func TestStorePrivateFilesAndCorruption(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "private")
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.expose(Exposure{DeviceID: "one", Kind: KindSwitch}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "bridge.json")} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s not private: %v", path, info.Mode())
		}
	}
	path := filepath.Join(dir, "bridge.json")
	if err = os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = openStore(dir); err == nil {
		t.Fatal("accepted public file")
	}
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{`, `{}`, `null`, `{"version":2}`, `{} {}`} {
		if err = os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = openStore(dir); err == nil {
			t.Fatalf("accepted corrupt store %q", data)
		}
		got, readErr := os.ReadFile(path)
		if readErr != nil || string(got) != data {
			t.Fatal("corrupt store was overwritten")
		}
	}
	if err = os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = openStore(dir); err == nil {
		t.Fatal("accepted public directory")
	}
}

func TestStoreFailureBeforeRenamePreservesPreviousFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "private")
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bridge.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.beforeRename = func() error {
		_, openErr := os.Open(filepath.Join(dir, "missing", "file"))
		return openErr
	}
	if _, err = s.expose(Exposure{DeviceID: "one", Kind: KindSwitch}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected filesystem failure, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("previous file changed")
	}
	if len(s.snapshot().Exposures) != 0 {
		t.Fatal("failed write changed in-memory state")
	}
	files, err := filepath.Glob(filepath.Join(dir, ".bridge-*.tmp"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary file not cleaned up")
	}
}

func TestStoreDirectorySyncFailurePreservesUncertainOutcome(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "private")
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.syncDir = func(*os.File) error { return os.ErrPermission }
	if _, err = s.expose(
		Exposure{DeviceID: "one", Kind: KindSwitch},
	); !errors.Is(err, errStoreDurabilityUncertain) ||
		!errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected uncertain outcome with cause, got %v", err)
	}
	if s.snapshot().Exposures["one"].AID != 2 {
		t.Fatal("in-memory state did not adopt visible rename")
	}
	reopened, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.snapshot().Exposures["one"].AID != 2 {
		t.Fatal("renamed file did not contain new allocation")
	}
	s.syncDir = nil
	second, err := s.expose(Exposure{DeviceID: "two", Kind: KindSwitch})
	if err != nil || second.AID != 3 {
		t.Fatalf("subsequent allocation = %+v, %v", second, err)
	}
}

func TestStoreRejectsCorruptAllocations(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "private")
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.expose(
		Exposure{DeviceID: "one", Kind: KindSwitch, Entities: []RoleAssignment{{Role: "on", EntityID: "a"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	original := s.snapshot()
	cases := map[string]func(*storeFile){
		"duplicate aid": func(f *storeFile) {
			f.Retired["two"] = RetiredAllocation{AID: f.Exposures["one"].AID, Slots: map[string]uint16{}}
		},
		"next aid too small": func(f *storeFile) { f.NextAID = 2 },
		"duplicate slot": func(f *storeFile) {
			f.Exposures["one"].Slots["on/b"] = 1
		},
		"missing assignment slot": func(f *storeFile) { delete(f.Exposures["one"].Slots, "on/a") },
		"invalid setup":           func(f *storeFile) { f.Setup.Code = "12345678" },
	}
	for name, mutate := range cases {
		data := cloneStoreFile(original)
		mutate(&data)
		encoded, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		path := filepath.Join(dir, "bridge.json")
		if writeErr := os.WriteFile(path, encoded, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		if _, openErr := openStore(dir); openErr == nil {
			t.Errorf("%s: accepted corrupt allocation", name)
		}
	}
}
