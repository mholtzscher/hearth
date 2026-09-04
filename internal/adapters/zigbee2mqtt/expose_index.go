package zigbee2mqtt

import (
	"strconv"
)

// exposeIndex normalizes Zigbee2MQTT expose structure, endpoint resolution, and
// Device-wide property claims so planners share one consistent inventory view.
type exposeIndex struct {
	roots          []indexedExpose
	propertyCounts map[string]int
}

// indexedExpose stores the effective numeric endpoint for its root. An
// unresolved scoped endpoint remains in the index with resolved == false;
// planners omit it without affecting unrelated roots.
type indexedExpose struct {
	expose   upstreamExpose
	endpoint int
	scoped   bool
	resolved bool
	order    int
}

const (
	upstreamExposeBinary  = "binary"
	upstreamExposeNumeric = "numeric"
)

// featureQuery matches one nested expose feature by type and name.
type featureQuery struct {
	Type string
	Name string
}

// newExposeIndex traverses every root and nested feature once. propertyCounts
// includes supported and unsupported exposes because one MQTT object property
// cannot safely identify two planned Entities. Root order remains inventory
// order.
func newExposeIndex(device upstreamDevice) exposeIndex {
	index := exposeIndex{propertyCounts: make(map[string]int)}
	if device.Definition == nil {
		return index
	}
	var countProperties func([]upstreamExpose)
	countProperties = func(items []upstreamExpose) {
		for _, expose := range items {
			if expose.Property != "" {
				index.propertyCounts[expose.Property]++
			}
			countProperties(expose.Features)
		}
	}
	countProperties(device.Definition.Exposes)
	for order, expose := range device.Definition.Exposes {
		endpoint, scoped, resolved := resolveEndpoint(expose.Endpoint, device.Endpoints)
		index.roots = append(index.roots, indexedExpose{
			expose: expose, endpoint: endpoint, scoped: scoped, resolved: resolved, order: order,
		})
	}
	return index
}

// Roots returns indexed root exposes of one type in inventory order.
func (index exposeIndex) Roots(exposeType string) []indexedExpose {
	var roots []indexedExpose
	for _, root := range index.roots {
		if root.expose.Type == exposeType {
			roots = append(roots, root)
		}
	}
	return roots
}

// UniqueFeature returns the single nested feature matching the query.
func (index exposeIndex) UniqueFeature(parent indexedExpose, query featureQuery) (upstreamExpose, bool) {
	var found upstreamExpose
	matches := 0
	for _, feature := range parent.expose.Features {
		if feature.Type == query.Type && feature.Name == query.Name {
			found = feature
			matches++
		}
	}
	return found, matches == 1
}

// PropertyUnique reports whether exactly one expose in the Device claims the property.
func (index exposeIndex) PropertyUnique(property string) bool {
	return index.propertyCounts[property] == 1
}

func resolveEndpoint(reference string, endpoints map[string]upstreamEndpoint) (int, bool, bool) {
	if reference == "" {
		return 0, false, true
	}
	matches := make(map[int]struct{})
	for key, endpoint := range endpoints {
		number, err := strconv.Atoi(key)
		if err != nil || number < 0 {
			continue
		}
		if key == reference || endpoint.Name == reference {
			matches[number] = struct{}{}
		}
	}
	if len(matches) != 1 {
		return 0, true, false
	}
	for number := range matches {
		return number, true, true
	}
	panic("unreachable endpoint resolution")
}
