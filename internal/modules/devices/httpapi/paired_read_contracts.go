package httpapi

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func validPairedInclude(include, units string) bool {
	return include == "" || (include == string(devices.UnitsCanonical) && units == "")
}

// Huma treats an explicitly empty query parameter as absent. Include has no
// empty value, and multiple selections would be ambiguous.
func invalidIncludeSelection(ctx huma.Context) bool {
	url := ctx.URL()
	values, present := url.Query()["include"]
	return present && (len(values) != 1 || values[0] == "")
}
