package esphome //nolint:testpackage // Exercises planning, projection, and command admission.

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestPlanCoverLockKeepsSiblingsAndCapabilities(t *testing.T) {
	t.Parallel()
	mac := MAC{2, 0, 0, 0, 0, 1}
	entities := []nativeapi.EntityInfo{
		{Type: nativeapi.EntityBinarySensor, Key: 1, Name: "Obstruction"},
		{Type: nativeapi.EntityLight, Key: 2, Name: "Light"},
		{Type: nativeapi.EntityCover, Key: 3, Name: "Door", SupportsPosition: true, SupportsStop: true},
		{Type: nativeapi.EntityLock, Key: 4, Name: "Lock remotes"},
	}
	plan, err := planDevice(mac, nativeapi.DeviceInfo{Name: "Garage"}, entities, Exposure{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Routes) != 4 || plan.Registration.Device.Kind != "cover" {
		t.Fatalf("registration=%+v", plan.Registration)
	}
	cover := descriptorByExternalID(t, plan, mac.String()+"/0/cover/Door")
	lock := descriptorByExternalID(t, plan, mac.String()+"/0/lock/Lock remotes")
	if cover.Type != "hearth.cover/v1" || lock.Type != "hearth.lock/v1" || lock.Name != "Lock remotes" {
		t.Fatalf("cover=%+v lock=%+v", cover, lock)
	}
	if string(cover.Support) != `{"state":{},"operations":{"set":{},"stop":{}}}` ||
		string(lock.Support) != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("support: %s, %s", cover.Support, lock.Support)
	}
	before, err := planDevice(mac, nativeapi.DeviceInfo{}, entities[:2], Exposure{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sibling := range before.Registration.Entities {
		after := descriptorByExternalID(t, plan, sibling.ExternalID)
		if !reflect.DeepEqual(sibling, after) {
			t.Fatalf("sibling identity changed: %+v -> %+v", sibling, after)
		}
	}
}

func TestPlanCoverLockExposureAndSafetyLimits(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		entity  nativeapi.EntityInfo
		expose  Exposure
		count   int
		support string
	}{
		{"no-position", nativeapi.EntityInfo{Type: nativeapi.EntityCover, Name: "Door"},
			Exposure{Include: map[EntityRef]struct{}{{Type: nativeapi.EntityCover, Name: "Door"}: {}}}, 0, ""},
		{"assumed-cover", nativeapi.EntityInfo{Type: nativeapi.EntityCover, Name: "Door",
			SupportsPosition: true, AssumedState: true}, Exposure{}, 0, ""},
		{"no-stop", nativeapi.EntityInfo{Type: nativeapi.EntityCover, Name: "Door", SupportsPosition: true},
			Exposure{}, 1, `{"state":{},"operations":{"set":{}}}`},
		{"code-lock", nativeapi.EntityInfo{Type: nativeapi.EntityLock, Name: "Lock", RequiresCode: true},
			Exposure{}, 1, `{"state":{},"operations":{}}`},
		{"assumed-lock", nativeapi.EntityInfo{Type: nativeapi.EntityLock, Name: "Lock", AssumedState: true},
			Exposure{}, 0, ""},
		{"excluded", nativeapi.EntityInfo{Type: nativeapi.EntityCover, Name: "Door", SupportsPosition: true},
			Exposure{Exclude: map[EntityRef]struct{}{{Type: nativeapi.EntityCover, Name: "Door"}: {}}}, 0, ""},
		{"diagnostic", nativeapi.EntityInfo{Type: nativeapi.EntityLock, Name: "Lock", Category: nativeapi.CategoryDiagnostic},
			Exposure{}, 0, ""},
		{"included", nativeapi.EntityInfo{Type: nativeapi.EntityLock, Name: "Lock", Category: nativeapi.CategoryDiagnostic},
			Exposure{Include: map[EntityRef]struct{}{{Type: nativeapi.EntityLock, Name: "Lock"}: {}}}, 1,
			`{"state":{},"operations":{"set":{}}}`},
		{"sub-device", nativeapi.EntityInfo{Type: nativeapi.EntityCover, Name: "Door", DeviceID: 1, SupportsPosition: true},
			Exposure{}, 0, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := planDevice(MAC{2, 0, 0, 0, 0, 1}, nativeapi.DeviceInfo{},
				[]nativeapi.EntityInfo{test.entity}, test.expose)
			if err != nil || len(plan.Routes) != test.count {
				t.Fatalf("plan=%+v, %v", plan, err)
			}
			if test.count == 1 && string(plan.Registration.Entities[0].Support) != test.support {
				t.Fatalf("support=%s", plan.Registration.Entities[0].Support)
			}
		})
	}
	for _, name := range []string{"cover/Door", "lock/Lock remotes"} {
		if _, err := ParseEntityRef(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCoverProjectionAndCoreMatching(t *testing.T) {
	t.Parallel()
	route := entityRoute{Kind: routeCover, Cover: coverSupport(true)}
	for _, test := range []struct {
		position float64
		motion   string
		want     string
	}{
		{0, "idle", `{"motion":"idle","position":0}`},
		{1, "idle", `{"motion":"idle","position":100}`},
		{0.345, "opening", `{"motion":"opening","position":35}`},
		{0.495, "closing", `{"motion":"closing","position":50}`},
		{-0.1, "idle", ""}, {1.1, "idle", ""}, {math.NaN(), "idle", ""}, {math.Inf(1), "idle", ""},
		{0.5, "unknown", ""},
	} {
		observation, ok, err := project(route, "ent_cover", nativeapi.StateUpdate{
			Type: nativeapi.EntityCover, Position: test.position, CoverOperation: test.motion,
		}, projectionTime())
		if test.want == "" {
			if ok || err == nil {
				t.Fatalf("invalid report produced State: %+v, %t, %v", observation, ok, err)
			}
		} else if err != nil || !ok || string(observation.Value) != test.want {
			t.Fatalf("projection=%s, %t, %v; want %s", observation.Value, ok, err, test.want)
		}
	}
	if !stateMatches(routeCover, []byte(`{"position":49,"motion":"opening"}`), []byte(`{"position":50}`)) ||
		stateMatches(routeCover, []byte(`{"position":48,"motion":"idle"}`), []byte(`{"position":50}`)) {
		t.Fatal("cover evidence does not use canonical one-point tolerance")
	}
}

func TestCoverAndLockAdmissionRejectInvalidOrUnsupportedOperations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ operation, parameters string }{
		{"set", `{"position":-1}`}, {"set", `{"position":101}`}, {"set", `{"position":50.5}`},
		{"set", `{}`}, {"stop", `{"position":0}`}, {"toggle", `{}`},
	} {
		if _, err := prepareCoverCommand(boundRoute{Kind: routeCover, Cover: coverSupport(true)},
			adapter.Command{OperationName: test.operation, Parameters: json.RawMessage(test.parameters)}); err == nil {
			t.Errorf("accepted %s %s", test.operation, test.parameters)
		}
	}
	if _, err := prepareCoverCommand(boundRoute{Kind: routeCover, Cover: coverSupport(false)},
		adapter.Command{OperationName: "stop", Parameters: []byte(`{}`)}); err == nil {
		t.Fatal("accepted unadvertised stop")
	}
	for _, control := range []bool{true, false} {
		target, native, err := prepareSet(boundRoute{Kind: routeLock, Lock: lockSupport(control)},
			[]byte(`{"value":"locked"}`))
		if !control {
			if err == nil {
				t.Fatal("accepted code-requiring lock control")
			}
		} else if err != nil || string(target) != `"locked"` || native != (nativeapi.LockCommand{Locked: true}) {
			t.Fatalf("lock command=%s, %+v, %v", target, native, err)
		}
	}
}
