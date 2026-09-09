package zigbee2mqtt //nolint:testpackage // Tests exercise package-private planDevice merge via canned contributions.

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// staticContribution returns one canned planner contribution without
// inspecting any device, so tests exercise only planDevice's generic merge
// contract and never concrete expose parsing.
func staticContribution(kind string, role plannerRole, entities ...entityPlan) plannerContribution {
	return plannerContribution{Kind: kind, Entities: entities, Role: role}
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
	plan, err := planDevice([]plannerContribution{
		staticContribution(upstreamDeviceKindLight, plannerRolePrimary, primary),
		staticContribution(upstreamDeviceKindRelay, plannerRolePrimary, loser),
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
	plan, err := planDevice([]plannerContribution{
		staticContribution(upstreamDeviceKindLight, plannerRolePrimary,
			mustStaticEntity(t, "primary-power", "primary-state")),
		staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental,
			mustStaticEntity(t, "first-a", "first-a-state"),
			mustStaticEntity(t, "first-b", "first-b-state")),
		staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental,
			mustStaticEntity(t, "second", "second-state")),
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
	plan, err := planDevice([]plannerContribution{
		staticContribution(upstreamDeviceKindLight, plannerRolePrimary),
		staticContribution(upstreamDeviceKindRelay, plannerRoleSupplemental),
		staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental,
			mustStaticEntity(t, "first", "first-state")),
		staticContribution(upstreamDeviceKindRelay, plannerRoleSupplemental,
			mustStaticEntity(t, "second", "second-state")),
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
// contribution that emits one key twice keeps one copy (leaking an ambiguous route)
// or keeps both copies (collapsing the merge into ambiguous_entity_plan
// instead of quietly dropping the duplicated family).
func TestPlanDeviceDropsSameContributionDuplicateKeys(t *testing.T) {
	t.Parallel()
	first := mustStaticEntity(t, "dup", "dup-a-state")
	first.Descriptor.ExternalID = "0x00124b0024abcdef/root/dup-a"
	second := mustStaticEntity(t, "dup", "dup-b-state")
	second.Descriptor.ExternalID = "0x00124b0024abcdef/root/dup-b"
	plan, err := planDevice([]plannerContribution{
		staticContribution(upstreamDeviceKindLight, plannerRolePrimary, first, second),
		staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental,
			mustStaticEntity(t, "kept", "kept-state")),
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
	_, err := planDevice([]plannerContribution{
		staticContribution(upstreamDeviceKindLight, plannerRolePrimary, primary),
		staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental, supplemental),
	})
	if err == nil {
		t.Fatal("colliding primary and supplemental keys were merged without rejection")
	}
	if code := planRejectionCode(t, err); code != rejectionAmbiguousEntityPlan {
		t.Fatalf("rejection code = %q, want %q", code, rejectionAmbiguousEntityPlan)
	}
}

// This test protects omitted-role rejection and fails if a contribution that
// omits its role (the plannerRole zero value) competes silently instead of
// rejecting the Device as invalid_descriptor before selection.
func TestPlanDeviceOmittedRoleRejectsInvalidDescriptor(t *testing.T) {
	t.Parallel()
	_, err := planDevice([]plannerContribution{
		staticContribution(upstreamDeviceKindLight, plannerRoleInvalid,
			mustStaticEntity(t, "orphan", "orphan-state")),
	})
	if err == nil {
		t.Fatal("omitted contribution role merged without rejection")
	}
	if code := planRejectionCode(t, err); code != rejectionInvalidDescriptor {
		t.Fatalf("rejection code = %q, want %q", code, rejectionInvalidDescriptor)
	}
}

// This test protects omitted-kind rejection and fails if a contribution that
// omits its Device kind merges silently instead of rejecting the Device as
// invalid_descriptor before selection.
func TestPlanDeviceOmittedKindRejectsInvalidDescriptor(t *testing.T) {
	t.Parallel()
	_, err := planDevice([]plannerContribution{
		staticContribution("", plannerRolePrimary,
			mustStaticEntity(t, "orphan", "orphan-state")),
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
	_, err := planDevice([]plannerContribution{
		staticContribution(upstreamDeviceKindLight, plannerRolePrimary),
		staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental),
	})
	if err == nil {
		t.Fatal("empty contributions registered a Device without rejection")
	}
	if code := planRejectionCode(t, err); code != rejectionNoEligibleEntity {
		t.Fatalf("rejection code = %q, want %q", code, rejectionNoEligibleEntity)
	}
}

// This test protects generic empty-merge attribution and fails if an empty
// merge is not attributed to the first matched primary family in profile
// order: a matched light contribution rejects as no_eligible_light even when
// a later relay also matched, a matched relay alone rejects as
// no_eligible_relay, and unmatched contributions fall back to
// no_eligible_entity. The defect would be a diagnostic that blames the wrong
// family without any mapping-specific root-type branch.
func TestPlanDeviceEmptyMergeAttributesStrongestMatchedPrimary(t *testing.T) {
	t.Parallel()
	matched := func(kind string, role plannerRole) plannerContribution {
		contribution := staticContribution(kind, role)
		contribution.Matched = true
		return contribution
	}
	for _, test := range []struct {
		name          string
		contributions []plannerContribution
		want          string
	}{
		{
			name: "matched light wins over matched relay",
			contributions: []plannerContribution{
				matched(upstreamDeviceKindLight, plannerRolePrimary),
				matched(upstreamDeviceKindRelay, plannerRolePrimary),
				staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental),
			},
			want: rejectionNoEligibleLight,
		},
		{
			name: "matched relay alone",
			contributions: []plannerContribution{
				staticContribution(upstreamDeviceKindLight, plannerRolePrimary),
				matched(upstreamDeviceKindRelay, plannerRolePrimary),
				staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental),
			},
			want: rejectionNoEligibleRelay,
		},
		{
			name: "unmatched supplementals only",
			contributions: []plannerContribution{
				staticContribution(upstreamDeviceKindLight, plannerRolePrimary),
				staticContribution(upstreamDeviceKindSensor, plannerRoleSupplemental),
			},
			want: rejectionNoEligibleEntity,
		},
		{
			name: "matched supplemental never attributes",
			contributions: []plannerContribution{
				staticContribution(upstreamDeviceKindLight, plannerRolePrimary),
				matched(upstreamDeviceKindSensor, plannerRoleSupplemental),
			},
			want: rejectionNoEligibleEntity,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := planDevice(test.contributions)
			if err == nil {
				t.Fatal("empty contributions registered a Device without rejection")
			}
			if code := planRejectionCode(t, err); code != test.want {
				t.Fatalf("rejection code = %q, want %q", code, test.want)
			}
		})
	}
}
