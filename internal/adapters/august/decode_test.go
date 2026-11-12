package august //nolint:testpackage // Exercises presence-aware private wire decoders.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestInventoryRejectsPartialAndConflictingIDs(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`null`, `[]`, `{"invalid":{}}`,
		fmt.Sprintf(`{"%s":{"LockName":"Front","UserType":"superuser"},"%s":null}`, testLockID, testOtherID),
		fmt.Sprintf(`{"%s":{"LockName":"Front","UserType":"superuser"},"%s":{"LockName":"Front","UserType":"user"}}`, testLockID, testLockID),
		fmt.Sprintf(`{"%s":{"LockName":"Front","UserType":"superuser"},"%s":{"LockName":"Front","UserType":"user"}}`, strings.Repeat("a", 32), strings.Repeat("A", 32)),
		`{} {}`,
	} {
		if _, err := decodeInventory([]byte(body)); failureCode(err) != codeInvalid {
			t.Fatalf("invalid listing accepted: %s", body)
		}
	}
	entries, err := decodeInventory(
		[]byte(
			fmt.Sprintf(
				`{"%s":{"LockName":"Front","UserType":"superuser"},"%s":{"LockName":"Front","UserType":"user"}}`,
				testLockID,
				testOtherID,
			),
		),
	)
	if err != nil || len(entries) != 2 || entries[0].ID != testLockID || entries[1].ID != testOtherID ||
		entries[1].UserType != "user" {
		t.Fatalf("duplicate names merged: %+v %v", entries, err)
	}
}

func TestStatusPreservesExplicitStatesAndMissingSiblings(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, test := range []struct{ wire, want string }{
		{"locked", "locked"}, {"unlocked", "unlocked"}, {"locking", "locking"}, {"unlocking", "unlocking"}, {"jammed", "jammed"}, {"unknown", "unknown"},
		{"kAugLockState_Locked", "locked"}, {"kAugLockState_UnknownStaticPosition", "jammed"}, {"unrecognized", ""},
	} {
		got, err := decodeStatus(
			[]byte(
				fmt.Sprintf(
					`{"status":%q,"doorState":"closed","battery":0,"dateTime":"2020-01-01T00:00:00Z"}`,
					test.wire,
				),
			),
			now,
		)
		if err != nil || got.DoorOpen == nil || got.DoorOpen.Value || got.Battery == nil || got.Battery.Value != 0 ||
			got.DoorOpen.SourceUpdatedAt == nil {
			t.Fatalf("valid zero/false/old timestamp lost for %s: %+v %v", test.wire, got, err)
		}
		if test.want == "" {
			if got.Lock != nil {
				t.Fatal("unrecognized State fabricated as unknown")
			}
		} else if got.Lock == nil || string(got.Lock.Value) != test.want {
			t.Fatal("incorrect State normalization")
		}
	}
	for _, body := range []string{`{}`, `{"status":null,"doorState":null,"battery":null}`, `{"status":true,"doorState":"init","battery":-1}`, `{"status":"unrecognized","doorState":"unknown","battery":1.1}`} {
		got, err := decodeStatus([]byte(body), now)
		if err != nil || got.Lock != nil || got.DoorOpen != nil || got.Battery != nil {
			t.Fatalf("missing/invalid fields fabricated: %+v %v", got, err)
		}
	}
	got, err := decodeStatus(
		[]byte(`{"status":"locked","doorState":"open","battery":0.7157,"dateTime":"invalid"}`),
		now,
	)
	if err != nil || got.Lock != nil || got.DoorOpen != nil || got.Battery == nil || got.Battery.Value != 71.57 ||
		got.Battery.SourceUpdatedAt != nil {
		t.Fatal("invalid timestamp contaminated siblings or was invented for battery")
	}
}

func TestDetailRequiresMatchingIdentityAndExplicitOnlineBridge(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		bridge string
		online bool
	}{
		{`{"operative":true,"status":{"current":"online"}}`, true},
		{`{"operative":true}`, false}, {`{"operative":false,"status":{"current":"online"}}`, false},
		{`{"operative":true,"status":{"current":"offline"}}`, false}, {`null`, false},
	} {
		body := fmt.Sprintf(
			`{"LockID":%q,"LockName":"Front","skuNumber":"AUG-SL05-M01-G01","currentFirmwareVersion":"1.1.30","battery":0.3764,"LockStatus":{"status":"unlocked","doorState":"closed"},"Bridge":%s,"pubsubChannel":"synthetic-private-channel","users":{"private":"discard"},"OfflineKeys":{"private":"discard"}}`,
			testLockID,
			test.bridge,
		)
		got, err := decodeDetail([]byte(body), inventoryEntry{ID: testLockID, UserType: "superuser"}, time.Now())
		if err != nil || got.Device.ID != testLockID || got.Device.Model != supportedModel ||
			got.Report.Online == nil ||
			*got.Report.Online != test.online ||
			got.Report.Lock == nil ||
			got.Report.Battery == nil ||
			got.Report.Battery.Value != 37.64 {
			t.Fatalf("detail decoding failed: %+v %v", got, err)
		}
		if _, err = decodeDetail(
			[]byte(body),
			inventoryEntry{ID: testOtherID},
			time.Now(),
		); failureCode(
			err,
		) != codeInvalid {
			t.Fatal("wrong-lock detail accepted")
		}
	}
}
