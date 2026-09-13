package ecowitt //nolint:testpackage // Registration tests exercise the package-private identity contract.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// snapshotFor builds the canonical route snapshot a Session would return for
// one configuration, using deterministic derived Entity IDs.
func snapshotFor(t *testing.T, config Config) routeSnapshot {
	t.Helper()
	plans := catalog(t)
	registrations := staticRegistrations(config, plans)
	bindings := make([]adapter.Binding, 0, len(registrations))
	for _, registration := range registrations {
		bindings = append(bindings, derivedBinding(registration))
	}
	snapshot, err := buildRouteSnapshot(plans, bindings)
	if err != nil {
		t.Fatalf("build route snapshot: %v", err)
	}
	return snapshot
}

// TestStaticRegistrationsMatchIdentityContract protects the two static Device
// slots: binding keys, external IDs, kind, configured names, and the catalog's
// per-slot Entity order and descriptors.
//
//nolint:gocognit // Table-driven contract test; the explicit field comparisons are the assertion.
func TestStaticRegistrationsMatchIdentityContract(t *testing.T) {
	t.Parallel()

	config := testConfig(t)
	plans := catalog(t)
	registrations := staticRegistrations(config, plans)
	if len(registrations) != 2 {
		t.Fatalf("registrations = %d, want exactly the two Device slots", len(registrations))
	}
	for index, expected := range []struct {
		slot deviceSlot
		name string
	}{
		{gatewaySlot, config.GatewayName},
		{outdoorArraySlot, config.OutdoorArrayName},
	} {
		registration := registrations[index]
		if registration.BindingKey != string(expected.slot) {
			t.Errorf("registration[%d] binding key = %q, want %q", index, registration.BindingKey, expected.slot)
		}
		if registration.Device.ExternalID == nil || *registration.Device.ExternalID != string(expected.slot) {
			t.Errorf(
				"registration[%d] device external ID = %v, want %q",
				index,
				registration.Device.ExternalID,
				expected.slot,
			)
		}
		if registration.Device.Kind != deviceKindSensor {
			t.Errorf("registration[%d] kind = %q, want sensor", index, registration.Device.Kind)
		}
		if registration.Device.Name != expected.name {
			t.Errorf("registration[%d] name = %q, want %q", index, registration.Device.Name, expected.name)
		}
		owned := plansForSlot(plans, expected.slot)
		if len(registration.Entities) != len(owned) {
			t.Fatalf("registration[%d] Entity count = %d, want %d", index, len(registration.Entities), len(owned))
		}
		for entityIndex, descriptor := range registration.Entities {
			if descriptor.Key != owned[entityIndex].EntityKey {
				t.Errorf("registration[%d] Entity[%d] key = %q, want %q",
					index, entityIndex, descriptor.Key, owned[entityIndex].EntityKey)
			}
			if descriptor.ExternalID != string(expected.slot)+"/"+owned[entityIndex].EntityKey {
				t.Errorf("registration[%d] Entity[%d] external ID = %q, want a slot-scoped key",
					index, entityIndex, descriptor.ExternalID)
			}
			if descriptor.Type != owned[entityIndex].Descriptor.Type {
				t.Errorf("registration[%d] Entity[%d] type = %q, want %q",
					index, entityIndex, descriptor.Type, owned[entityIndex].Descriptor.Type)
			}
		}
	}
}

// TestStaticRegistrationIdentityIsStableAcrossDisplayNamesAndRestart protects
// canonical identity: display names, PASSKEY, topic, and firmware never
// participate, and a later restart with a different display name keeps the same
// Binding and Entity identity.
func TestStaticRegistrationIdentityIsStableAcrossDisplayNamesAndRestart(t *testing.T) {
	t.Parallel()

	first := testConfig(t)
	second := testConfig(t)
	second.GatewayName = "Renamed Gateway"
	second.OutdoorArrayName = "Renamed Array"
	second.MQTTTopic = "ecowitt/000000000000"
	second.ExpectedPasskey = [16]byte{0xff, 0xee}

	firstRegistrations := staticRegistrations(first, catalog(t))
	secondRegistrations := staticRegistrations(second, catalog(t))
	if len(firstRegistrations) != len(secondRegistrations) {
		t.Fatal("registration counts differ across restart")
	}
	for index := range firstRegistrations {
		before := firstRegistrations[index]
		after := secondRegistrations[index]
		if before.BindingKey != after.BindingKey {
			t.Fatalf("registration[%d] binding key changed across restart", index)
		}
		if before.Device.Kind != after.Device.Kind {
			t.Fatalf("registration[%d] device kind changed across restart", index)
		}
		if *before.Device.ExternalID != *after.Device.ExternalID {
			t.Fatalf("registration[%d] device external ID changed across restart", index)
		}
		if before.Device.Name == after.Device.Name {
			t.Fatalf("registration[%d] display name did not change", index)
		}
		beforeEntities, err := json.Marshal(before.Entities)
		if err != nil {
			t.Fatal(err)
		}
		afterEntities, err := json.Marshal(after.Entities)
		if err != nil {
			t.Fatal(err)
		}
		if string(beforeEntities) != string(afterEntities) {
			t.Fatalf("registration[%d] Entity identity changed across restart:\n%s\n%s",
				index, beforeEntities, afterEntities)
		}
	}
}

// TestBuildRouteSnapshotJoinsCanonicalEntityIDs protects the traversal order
// and the single canonical Entity ID per catalog plan.
func TestBuildRouteSnapshotJoinsCanonicalEntityIDs(t *testing.T) {
	t.Parallel()

	plans := catalog(t)
	snapshot := snapshotFor(t, testConfig(t))
	if len(snapshot.entities) != len(plans) {
		t.Fatalf("snapshot size = %d, want %d", len(snapshot.entities), len(plans))
	}
	seen := make(map[string]struct{}, len(snapshot.entities))
	for index, route := range snapshot.entities {
		if route.Index != index {
			t.Fatalf("route[%d] index = %d", index, route.Index)
		}
		if route.EntityKey != plans[index].EntityKey {
			t.Fatalf("route[%d] key = %q, want %q", index, route.EntityKey, plans[index].EntityKey)
		}
		if route.Slot != plans[index].Slot {
			t.Fatalf("route[%d] slot = %q, want %q", index, route.Slot, plans[index].Slot)
		}
		if route.EntityID == "" {
			t.Fatalf("route[%d] has no canonical Entity ID", index)
		}
		if _, duplicate := seen[route.EntityID]; duplicate {
			t.Fatalf("route[%d] repeats canonical Entity ID %q", index, route.EntityID)
		}
		seen[route.EntityID] = struct{}{}
	}
}

// TestBuildRouteSnapshotRejectsMismatchedBindings protects the registration
// boundary: a Session response that does not match the request is rejected
// rather than publishing against a guessed Entity ID.
func TestBuildRouteSnapshotRejectsMismatchedBindings(t *testing.T) {
	t.Parallel()

	plans := catalog(t)
	registrations := staticRegistrations(testConfig(t), plans)
	valid := []adapter.Binding{
		derivedBinding(registrations[0]),
		derivedBinding(registrations[1]),
	}
	for _, testCase := range []struct {
		name     string
		bindings []adapter.Binding
	}{
		{name: "too few bindings", bindings: valid[:1]},
		{name: "wrong binding order", bindings: []adapter.Binding{valid[1], valid[0]}},
		{
			name: "omitted Entity",
			bindings: []adapter.Binding{
				{
					BindingKey: valid[0].BindingKey,
					DeviceID:   valid[0].DeviceID,
					Entities:   valid[0].Entities[:1],
				},
				valid[1],
			},
		},
		{
			name: "extra Entity",
			bindings: []adapter.Binding{
				{
					BindingKey: valid[0].BindingKey,
					DeviceID:   valid[0].DeviceID,
					Entities: append(
						append([]adapter.EntityBinding(nil), valid[0].Entities...),
						adapter.EntityBinding{Key: "extra", EntityID: "ent-extra", Enabled: true},
					),
				},
				valid[1],
			},
		},
		{
			name: "empty Entity ID",
			bindings: []adapter.Binding{
				{
					BindingKey: valid[0].BindingKey,
					DeviceID:   valid[0].DeviceID,
					Entities: []adapter.EntityBinding{
						{Key: "indoor-temperature", EntityID: ""},
						valid[0].Entities[1], valid[0].Entities[2], valid[0].Entities[3],
					},
				},
				valid[1],
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := buildRouteSnapshot(plans, testCase.bindings); !errors.Is(err, errRegistrationMismatch) {
				t.Fatalf("build route snapshot error = %v, want errRegistrationMismatch", err)
			}
		})
	}
}
