package zigbee2mqtt //nolint:testpackage // Test contribution merging directly, without fake planners.

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func mustStaticEntity(t *testing.T, key, property string) entityPlan {
	t.Helper()
	plan, err := newTemperaturePlan(
		adapter.EntityMetadata{
			Key:        key,
			ExternalID: "0x00124b0024abcdef/root/" + key,
			Name:       key + " Name",
		},
		property,
		true,
	)
	if err != nil {
		t.Fatalf("static Entity %q was rejected: %v", key, err)
	}
	return plan
}

func planRejectionCode(t *testing.T, err error) string {
	t.Helper()
	planErr, ok := errors.AsType[*devicePlanError](err)
	if !ok {
		t.Fatalf("plan error = %#v, want *devicePlanError", err)
	}
	return planErr.code
}

// Later primary contributions must not replace or join the first eligible one.
func TestPlanDeviceFirstPrimaryWins(t *testing.T) {
	t.Parallel()
	primary := mustStaticEntity(t, "light-power", "light-state")
	loser := mustStaticEntity(t, "relay-power", "relay-state")
	plan, err := mergeDeviceContributions(exposeIndex{}, []plannerContribution{
		{Kind: upstreamDeviceKindLight, Role: plannerRolePrimary, Entities: []entityPlan{primary}},
		{Kind: upstreamDeviceKindRelay, Role: plannerRolePrimary, Entities: []entityPlan{loser}},
	})
	if err != nil {
		t.Fatalf("plan was rejected: %v", err)
	}
	if plan.Kind != upstreamDeviceKindLight {
		t.Fatalf("Device kind = %q, want %q", plan.Kind, upstreamDeviceKindLight)
	}
	if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, []string{"light-power"}) {
		t.Fatalf("Entity keys = %v, want only the winning primary", got)
	}
}

// Supplemental contributions append after the selected primary, in order.
func TestPlanDeviceSupplementalsAppendAfterPrimaryInOrder(t *testing.T) {
	t.Parallel()
	plan, err := mergeDeviceContributions(exposeIndex{}, []plannerContribution{
		{
			Kind: upstreamDeviceKindLight, Role: plannerRolePrimary,
			Entities: []entityPlan{mustStaticEntity(t, "primary-power", "primary-state")},
		},
		{
			Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental,
			Entities: []entityPlan{
				mustStaticEntity(t, "first-a", "first-a-state"),
				mustStaticEntity(t, "first-b", "first-b-state"),
			},
		},
		{
			Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental,
			Entities: []entityPlan{mustStaticEntity(t, "second", "second-state")},
		},
	})
	if err != nil {
		t.Fatalf("plan was rejected: %v", err)
	}
	if plan.Kind != upstreamDeviceKindLight {
		t.Fatalf("Device kind = %q, want %q", plan.Kind, upstreamDeviceKindLight)
	}
	want := []string{"primary-power", "first-a", "first-b", "second"}
	if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entity keys = %v, want %v", got, want)
	}
}

// Empty contributions cannot steal kind selection from a non-empty one.
func TestPlanDeviceSupplementalOnlyUsesFirstNonEmptyKind(t *testing.T) {
	t.Parallel()
	plan, err := mergeDeviceContributions(exposeIndex{}, []plannerContribution{
		{Kind: upstreamDeviceKindLight, Role: plannerRolePrimary},
		{Kind: upstreamDeviceKindRelay, Role: plannerRoleSupplemental},
		{
			Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental,
			Entities: []entityPlan{mustStaticEntity(t, "first", "first-state")},
		},
		{
			Kind: upstreamDeviceKindRelay, Role: plannerRoleSupplemental,
			Entities: []entityPlan{mustStaticEntity(t, "second", "second-state")},
		},
	})
	if err != nil {
		t.Fatalf("plan was rejected: %v", err)
	}
	if plan.Kind != upstreamDeviceKindSensor {
		t.Fatalf("Device kind = %q, want %q", plan.Kind, upstreamDeviceKindSensor)
	}
	if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("Entity keys = %v, want both supplementals in order", got)
	}
}

// Duplicates within one contribution omit all copies, not the whole Device.
func TestPlanDeviceDropsSameContributionDuplicateKeys(t *testing.T) {
	t.Parallel()
	first := mustStaticEntity(t, "dup", "dup-a-state")
	first.Descriptor.ExternalID = "0x00124b0024abcdef/root/dup-a"
	second := mustStaticEntity(t, "dup", "dup-b-state")
	second.Descriptor.ExternalID = "0x00124b0024abcdef/root/dup-b"
	plan, err := mergeDeviceContributions(exposeIndex{}, []plannerContribution{
		{Kind: upstreamDeviceKindLight, Role: plannerRolePrimary, Entities: []entityPlan{first, second}},
		{
			Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental,
			Entities: []entityPlan{mustStaticEntity(t, "kept", "kept-state")},
		},
	})
	if err != nil {
		t.Fatalf("plan was rejected: %v", err)
	}
	if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, []string{"kept"}) {
		t.Fatalf("Entity keys = %v, want only the supplemental survivor", got)
	}
}

// Cross-contribution collisions reject rather than creating ambiguous routes.
func TestPlanDevicePrimarySupplementalCollisionIsAmbiguous(t *testing.T) {
	t.Parallel()
	primary := mustStaticEntity(t, "shared", "primary-state")
	primary.Descriptor.ExternalID = "0x00124b0024abcdef/root/primary-shared"
	supplemental := mustStaticEntity(t, "shared", "supplemental-state")
	supplemental.Descriptor.ExternalID = "0x00124b0024abcdef/root/supplemental-shared"
	_, err := mergeDeviceContributions(exposeIndex{}, []plannerContribution{
		{Kind: upstreamDeviceKindLight, Role: plannerRolePrimary, Entities: []entityPlan{primary}},
		{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental, Entities: []entityPlan{supplemental}},
	})
	if code := planRejectionCode(t, err); code != rejectionAmbiguousEntityPlan {
		t.Fatalf("rejection code = %q, want %q", code, rejectionAmbiguousEntityPlan)
	}
}

// Incomplete contributions must not silently participate in the merge.
func TestPlanDeviceOmittedRoleRejectsInvalidDescriptor(t *testing.T) {
	t.Parallel()
	_, err := mergeDeviceContributions(exposeIndex{}, []plannerContribution{
		{Kind: upstreamDeviceKindLight, Entities: []entityPlan{mustStaticEntity(t, "orphan", "orphan-state")}},
	})
	if code := planRejectionCode(t, err); code != rejectionInvalidDescriptor {
		t.Fatalf("rejection code = %q, want %q", code, rejectionInvalidDescriptor)
	}
}

func TestPlanDeviceOmittedKindRejectsInvalidDescriptor(t *testing.T) {
	t.Parallel()
	_, err := mergeDeviceContributions(exposeIndex{}, []plannerContribution{
		{Role: plannerRolePrimary, Entities: []entityPlan{mustStaticEntity(t, "orphan", "orphan-state")}},
	})
	if code := planRejectionCode(t, err); code != rejectionInvalidDescriptor {
		t.Fatalf("rejection code = %q, want %q", code, rejectionInvalidDescriptor)
	}
}

// An empty merge must not register an Entity-less Device.
func TestPlanDeviceEmptyContributionsRejectNoEligibleEntity(t *testing.T) {
	t.Parallel()
	_, err := mergeDeviceContributions(exposeIndex{}, []plannerContribution{
		{Kind: upstreamDeviceKindLight, Role: plannerRolePrimary},
		{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental},
	})
	if code := planRejectionCode(t, err); code != rejectionNoEligibleEntity {
		t.Fatalf("rejection code = %q, want %q", code, rejectionNoEligibleEntity)
	}
}
