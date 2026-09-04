package zigbee2mqtt //nolint:testpackage // Tests exercise package-private planDevice merge via a fake planner.

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// staticPlanner is a deterministic fake devicePlanner. It returns one canned
// contribution without inspecting the input, so tests exercise only
// planDevice's generic merge contract and never concrete expose parsing.
type staticPlanner struct {
	kind     string
	role     plannerRole
	entities []entityPlan
}

func (planner staticPlanner) Plan(devicePlanningInput) plannerContribution {
	return plannerContribution{Kind: planner.kind, Entities: planner.entities, Role: planner.role}
}

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

func staticInput() devicePlanningInput {
	return devicePlanningInput{IEEE: "0x00124b0024abcdef"}
}

func planRejectionCode(t *testing.T, err error) string {
	t.Helper()
	planErr, ok := errors.AsType[*devicePlanError](err)
	if !ok {
		t.Fatalf("plan error = %#v, want *devicePlanError", err)
	}
	return planErr.code
}

// This test protects first-non-empty-primary-wins precedence and fails if a
// later primary contribution overrides the winner or is concatenated into the
// merged Device alongside it.
func TestPlanDeviceFirstPrimaryWins(t *testing.T) {
	t.Parallel()
	primary := mustStaticEntity(t, "light-power", "light-state")
	loser := mustStaticEntity(t, "relay-power", "relay-state")
	plan, err := planDevice(staticInput(), []devicePlanner{
		staticPlanner{kind: upstreamDeviceKindLight, role: plannerRolePrimary, entities: []entityPlan{primary}},
		staticPlanner{kind: upstreamDeviceKindRelay, role: plannerRolePrimary, entities: []entityPlan{loser}},
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

// This test protects supplemental merge order and fails if a supplemental
// contribution is dropped, prepended before the selected primary, or merged
// out of planner order.
func TestPlanDeviceSupplementalsAppendAfterPrimaryInOrder(t *testing.T) {
	t.Parallel()
	plan, err := planDevice(staticInput(), []devicePlanner{
		staticPlanner{
			kind:     upstreamDeviceKindLight,
			role:     plannerRolePrimary,
			entities: []entityPlan{mustStaticEntity(t, "primary-power", "primary-state")},
		},
		staticPlanner{
			kind: upstreamDeviceKindSensor,
			role: plannerRoleSupplemental,
			entities: []entityPlan{
				mustStaticEntity(t, "first-a", "first-a-state"),
				mustStaticEntity(t, "first-b", "first-b-state"),
			},
		},
		staticPlanner{
			kind:     upstreamDeviceKindSensor,
			role:     plannerRoleSupplemental,
			entities: []entityPlan{mustStaticEntity(t, "second", "second-state")},
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

// This test protects supplemental-only Device kind selection and fails if an
// empty supplemental steals precedence, the last supplemental kind wins, or
// the merge falls back to the default sensor kind instead of the first
// non-empty supplemental kind.
func TestPlanDeviceSupplementalOnlyUsesFirstNonEmptyKind(t *testing.T) {
	t.Parallel()
	plan, err := planDevice(staticInput(), []devicePlanner{
		staticPlanner{kind: upstreamDeviceKindLight, role: plannerRolePrimary},
		staticPlanner{kind: upstreamDeviceKindRelay, role: plannerRoleSupplemental},
		staticPlanner{
			kind:     upstreamDeviceKindSensor,
			role:     plannerRoleSupplemental,
			entities: []entityPlan{mustStaticEntity(t, "first", "first-state")},
		},
		staticPlanner{
			kind:     upstreamDeviceKindRelay,
			role:     plannerRoleSupplemental,
			entities: []entityPlan{mustStaticEntity(t, "second", "second-state")},
		},
	})
	if err != nil {
		t.Fatalf("plan was rejected: %v", err)
	}
	if plan.Kind != upstreamDeviceKindSensor {
		t.Fatalf("Device kind = %q, want %q", plan.Kind, upstreamDeviceKindSensor)
	}
	if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("Entity keys = %v, want both supplementals in planner order", got)
	}
}

// This test protects same-contribution duplicate-key removal and fails if a
// planner that emits one key twice keeps one copy (leaking an ambiguous route)
// or keeps both copies (collapsing the merge into ambiguous_entity_plan
// instead of quietly dropping the duplicated family).
func TestPlanDeviceDropsSameContributionDuplicateKeys(t *testing.T) {
	t.Parallel()
	first := mustStaticEntity(t, "dup", "dup-a-state")
	first.Descriptor.ExternalID = "0x00124b0024abcdef/root/dup-a"
	second := mustStaticEntity(t, "dup", "dup-b-state")
	second.Descriptor.ExternalID = "0x00124b0024abcdef/root/dup-b"
	plan, err := planDevice(staticInput(), []devicePlanner{
		staticPlanner{
			kind:     upstreamDeviceKindLight,
			role:     plannerRolePrimary,
			entities: []entityPlan{first, second},
		},
		staticPlanner{
			kind:     upstreamDeviceKindSensor,
			role:     plannerRoleSupplemental,
			entities: []entityPlan{mustStaticEntity(t, "kept", "kept-state")},
		},
	})
	if err != nil {
		t.Fatalf("plan was rejected: %v", err)
	}
	if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, []string{"kept"}) {
		t.Fatalf("Entity keys = %v, want only the supplemental survivor", got)
	}
}

// This test protects cross-contribution collision rejection and fails if a key
// shared by the selected primary and a supplemental contribution merges
// silently (creating two Entities with one route) instead of rejecting the
// Device as ambiguous_entity_plan.
func TestPlanDevicePrimarySupplementalCollisionIsAmbiguous(t *testing.T) {
	t.Parallel()
	primary := mustStaticEntity(t, "shared", "primary-state")
	primary.Descriptor.ExternalID = "0x00124b0024abcdef/root/primary-shared"
	supplemental := mustStaticEntity(t, "shared", "supplemental-state")
	supplemental.Descriptor.ExternalID = "0x00124b0024abcdef/root/supplemental-shared"
	_, err := planDevice(staticInput(), []devicePlanner{
		staticPlanner{
			kind:     upstreamDeviceKindLight,
			role:     plannerRolePrimary,
			entities: []entityPlan{primary},
		},
		staticPlanner{
			kind:     upstreamDeviceKindSensor,
			role:     plannerRoleSupplemental,
			entities: []entityPlan{supplemental},
		},
	})
	if err == nil {
		t.Fatal("colliding primary and supplemental keys were merged without rejection")
	}
	if code := planRejectionCode(t, err); code != rejectionAmbiguousEntityPlan {
		t.Fatalf("rejection code = %q, want %q", code, rejectionAmbiguousEntityPlan)
	}
}

// This test protects omitted-role rejection and fails if a planner that
// omits its contribution role (the plannerRole zero value) competes silently
// instead of rejecting the Device as invalid_descriptor before selection.
func TestPlanDeviceOmittedRoleRejectsInvalidDescriptor(t *testing.T) {
	t.Parallel()
	_, err := planDevice(staticInput(), []devicePlanner{
		staticPlanner{
			kind:     upstreamDeviceKindLight,
			entities: []entityPlan{mustStaticEntity(t, "orphan", "orphan-state")},
		},
	})
	if err == nil {
		t.Fatal("omitted contribution role merged without rejection")
	}
	if code := planRejectionCode(t, err); code != rejectionInvalidDescriptor {
		t.Fatalf("rejection code = %q, want %q", code, rejectionInvalidDescriptor)
	}
}

// This test protects omitted-kind rejection and fails if a planner that omits
// its Device kind merges silently instead of rejecting the Device as
// invalid_descriptor before selection.
func TestPlanDeviceOmittedKindRejectsInvalidDescriptor(t *testing.T) {
	t.Parallel()
	_, err := planDevice(staticInput(), []devicePlanner{
		staticPlanner{
			role:     plannerRolePrimary,
			entities: []entityPlan{mustStaticEntity(t, "orphan", "orphan-state")},
		},
	})
	if err == nil {
		t.Fatal("omitted contribution kind merged without rejection")
	}
	if code := planRejectionCode(t, err); code != rejectionInvalidDescriptor {
		t.Fatalf("rejection code = %q, want %q", code, rejectionInvalidDescriptor)
	}
}

// This test protects empty-merge rejection and fails if a Device with no
// eligible contribution registers an Entity-less Device instead of rejecting
// it as no_eligible_entity.
func TestPlanDeviceEmptyContributionsRejectNoEligibleEntity(t *testing.T) {
	t.Parallel()
	_, err := planDevice(staticInput(), []devicePlanner{
		staticPlanner{kind: upstreamDeviceKindLight, role: plannerRolePrimary},
		staticPlanner{kind: upstreamDeviceKindSensor, role: plannerRoleSupplemental},
	})
	if err == nil {
		t.Fatal("empty contributions registered a Device without rejection")
	}
	if code := planRejectionCode(t, err); code != rejectionNoEligibleEntity {
		t.Fatalf("rejection code = %q, want %q", code, rejectionNoEligibleEntity)
	}
}
