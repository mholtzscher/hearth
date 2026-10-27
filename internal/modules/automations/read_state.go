package automations

import "github.com/mholtzscher/hearth/internal/modules/devices"

type ReadStateUnusablePolicy string

const (
	ReadStateFail ReadStateUnusablePolicy = "fail"
	ReadStateNull ReadStateUnusablePolicy = "null"
)

type ReadStateStep struct {
	EntityID      devices.EntityID
	Pointer       string
	MaxAgeSeconds *int64
	OnUnusable    ReadStateUnusablePolicy
}

func (ReadStateStep) isStepBody() {}

func normalizeReadStateStep(id StepID, body ReadStateStep) (Step, error) {
	if _, err := devices.ParseEntityID(string(body.EntityID)); err != nil {
		return Step{}, invalid("invalid State Entity")
	}
	if err := ValidateJSONPointer(body.Pointer); err != nil {
		return Step{}, invalid("invalid State pointer")
	}
	if body.MaxAgeSeconds != nil {
		age := *body.MaxAgeSeconds
		if age < 1 || age > automationConditionMaxAgeSeconds {
			return Step{}, invalid("invalid maximum State age")
		}
		body.MaxAgeSeconds = &age
	}
	if body.OnUnusable == "" {
		body.OnUnusable = ReadStateFail
	}
	if body.OnUnusable != ReadStateFail && body.OnUnusable != ReadStateNull {
		return Step{}, invalid("invalid State unusable policy")
	}
	return Step{ID: id, Body: body}, nil
}
