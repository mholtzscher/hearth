package devices //nolint:testpackage // Tests share canonical domain identities.

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	testAreaID = LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	testTagA   = LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	testTagB   = LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789ad")
)

func TestLabelPreparationNormalizesNamesKindsAndIDs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.FixedZone("x", 3600))
	record, err := PrepareCreateLabel(CreateLabelParams{Name: " Living Room ", Kind: LabelKindArea}, testAreaID, now)
	if err != nil || record.Label.Name != "Living Room" || record.NameKey != "living room" ||
		record.Label.CreatedAt.Location() != time.UTC {
		t.Fatalf("prepared = %#v, %v", record, err)
	}
	for name, test := range map[string]struct {
		params CreateLabelParams
		id     LabelID
		now    time.Time
	}{
		"empty name":   {CreateLabelParams{Name: " ", Kind: LabelKindTag}, testAreaID, now},
		"long name":    {CreateLabelParams{Name: strings.Repeat("x", 129), Kind: LabelKindTag}, testAreaID, now},
		"control":      {CreateLabelParams{Name: "a\nb", Kind: LabelKindTag}, testAreaID, now},
		"unknown kind": {CreateLabelParams{Name: "Floor", Kind: "floor"}, testAreaID, now},
		"bad ID":       {CreateLabelParams{Name: "Den", Kind: LabelKindArea}, "dev_x", now},
		"zero time":    {CreateLabelParams{Name: "Den", Kind: LabelKindArea}, testAreaID, time.Time{}},
	} {
		if _, err = PrepareCreateLabel(test.params, test.id, test.now); !errors.Is(err, ErrInvalidLabel) {
			t.Fatalf("%s = %v", name, err)
		}
	}
}

func TestMetadataPatchPreparationCanonicalizesLabelEdits(t *testing.T) {
	t.Parallel()
	area := testAreaID
	input := []LabelID{testTagB, testTagA, testTagB}
	prepared, err := PrepareEntityPatch(EntityPatch{
		AreaEdit: &AreaEdit{AreaID: &area},
		TagEdit:  &TagEdit{TagIDs: input},
	})
	if err != nil || *prepared.AreaEdit.AreaID != testAreaID ||
		!reflect.DeepEqual(prepared.TagEdit.TagIDs, []LabelID{testTagA, testTagB}) {
		t.Fatalf("prepared = %#v, %v", prepared, err)
	}
	area = "changed"
	input[0] = "changed"
	if *prepared.AreaEdit.AreaID != testAreaID || prepared.TagEdit.TagIDs[1] != testTagB {
		t.Fatal("prepared patch aliases caller memory")
	}
	if cleared, clearErr := PrepareDevicePatch(DevicePatch{
		AreaEdit: &AreaEdit{}, TagEdit: &TagEdit{},
	}); clearErr != nil || cleared.AreaEdit.AreaID != nil || len(cleared.TagEdit.TagIDs) != 0 {
		t.Fatalf("clear = %#v, %v", cleared, clearErr)
	}
	tooMany := make([]LabelID, 0, MaxTagsPerTarget+1)
	for index := range MaxTagsPerTarget + 1 {
		tooMany = append(tooMany, LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789"+twoHex(index)))
	}
	bad := LabelID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	for name, patch := range map[string]DevicePatch{
		"empty":    {},
		"bad area": {AreaEdit: &AreaEdit{AreaID: &bad}},
		"bad tag":  {TagEdit: &TagEdit{TagIDs: []LabelID{bad}}},
		"too many": {TagEdit: &TagEdit{TagIDs: tooMany}},
	} {
		if _, err = PrepareDevicePatch(patch); !errors.Is(err, ErrInvalidMetadataPatch) {
			t.Fatalf("%s = %v", name, err)
		}
	}
}

func twoHex(value int) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[value/16], digits[value%16]})
}

func TestEntitySelectorPreparationIsCanonicalAndBounded(t *testing.T) {
	t.Parallel()
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	selector, err := PrepareEntitySelector(catalog, EntitySelector{
		Labels: []LabelID{testTagB, testTagA, testTagB},
		Types:  []EntityTypeID{EntityTypePowerV1, EntityTypeBrightnessV1, EntityTypePowerV1},
	})
	if err != nil || !reflect.DeepEqual(selector, EntitySelector{
		Labels: []LabelID{testTagA, testTagB},
		Types:  []EntityTypeID{EntityTypeBrightnessV1, EntityTypePowerV1},
	}) {
		t.Fatalf("prepared = %#v, %v", selector, err)
	}
	if zero, zeroErr := PrepareEntitySelector(
		catalog,
		EntitySelector{Labels: []LabelID{}},
	); zeroErr != nil ||
		!zero.IsZero() ||
		zero.Labels != nil {
		t.Fatalf("empty selector = %#v, %v", zero, zeroErr)
	}
	labels := make([]LabelID, 0, MaxSelectorLabels+1)
	for index := range MaxSelectorLabels + 1 {
		labels = append(labels, LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789"+twoHex(index)))
	}
	for name, invalid := range map[string]EntitySelector{
		"bad label":    {Labels: []LabelID{"kitchen"}},
		"too many":     {Labels: labels},
		"unknown type": {Types: []EntityTypeID{"hearth.unknown/v1"}},
	} {
		if _, err = PrepareEntitySelector(catalog, invalid); !errors.Is(err, ErrInvalidEntitySelector) {
			t.Fatalf("%s = %v", name, err)
		}
	}
}
