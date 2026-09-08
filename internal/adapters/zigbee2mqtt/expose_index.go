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
	upstreamExposeBinary    = "binary"
	upstreamExposeNumeric   = "numeric"
	upstreamExposeComposite = "composite"
	upstreamExposeEnum      = "enum"
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

// UniqueRoot returns the single top-level expose matching the type and name.
// Device-root settings and actions (power_on_behavior, effect, linkquality)
// are allowlisted by exact type and name; duplicates are ambiguous and omit
// the capability without affecting valid siblings.
func (index exposeIndex) UniqueRoot(exposeType, name string) (indexedExpose, bool) {
	var found indexedExpose
	matches := 0
	for _, root := range index.roots {
		if root.expose.Type == exposeType && root.expose.Name == name {
			found = root
			matches++
		}
	}
	return found, matches == 1
}

// PropertyUnique reports whether exactly one expose in the Device claims the property.
func (index exposeIndex) PropertyUnique(property string) bool {
	return index.propertyCounts[property] == 1
}

// colorPropertyShareable reports whether one color composite candidate may
// claim its property without guessing. The index is walked recursively: every
// Device-wide claim of the same property at any nesting depth disqualifies,
// except direct color composites inside the same resolved light root. Within
// that exception one XY and one HS may share the property: either
// representation claimed more than once invalidates the shared ownership for
// every candidate on that property, so a duplicate HS also omits the paired
// XY. An invalid sibling therefore never suppresses the valid representation
// once ownership is established: ownership counts raw expose claims of both
// representations before candidate semantic validation.
func (index exposeIndex) colorPropertyShareable(
	root indexedExpose,
	candidate upstreamExpose,
) bool {
	if candidate.Property == "" {
		return false
	}
	xyClaims, hsClaims := countDirectColorClaims(root.expose, candidate.Property)
	if xyClaims > 1 || hsClaims > 1 {
		return false
	}
	if candidate.Name == upstreamColorXYName && xyClaims != 1 {
		return false
	}
	if candidate.Name == upstreamColorHSName && hsClaims != 1 {
		return false
	}
	for _, other := range index.roots {
		if !colorClaimsShareable(other.expose, 0, other.order == root.order, candidate.Property) {
			return false
		}
	}
	return true
}

// countDirectColorClaims counts the root's direct XY and HS features claiming
// the shared property, regardless of which representation is being validated.
func countDirectColorClaims(root upstreamExpose, property string) (int, int) {
	xyClaims, hsClaims := 0, 0
	for _, feature := range root.Features {
		if feature.Type != upstreamExposeComposite || feature.Property != property {
			continue
		}
		switch feature.Name {
		case upstreamColorXYName:
			xyClaims++
		case upstreamColorHSName:
			hsClaims++
		}
	}
	return xyClaims, hsClaims
}

// colorClaimsShareable reports whether every claim of the property under one
// root expose is a direct same-root color sibling sharing the property.
// The subtree is walked recursively so nested descendant claims disqualify.
func colorClaimsShareable(expose upstreamExpose, depth int, sameRoot bool, property string) bool {
	directSibling := sameRoot && depth == 1 &&
		expose.Type == upstreamExposeComposite && isColorCompositeName(expose.Name)
	if expose.Property == property && !directSibling {
		return false
	}
	for _, child := range expose.Features {
		if !colorClaimsShareable(child, depth+1, sameRoot, property) {
			return false
		}
	}
	return true
}

// propertyHasForeignClaim reports whether any Device expose at any nesting
// depth claims the property with a different meaning than the color-mode
// companion: anything other than an enum expose named color_mode. Such a
// collision omits the affected color and mode capabilities instead of
// guessing.
func (index exposeIndex) propertyHasForeignClaim(property string) bool {
	for _, root := range index.roots {
		if exposeHasForeignClaim(root.expose, property) {
			return true
		}
	}
	return false
}

// exposeHasForeignClaim reports whether the expose subtree claims the
// property with a different meaning than the color-mode companion.
func exposeHasForeignClaim(expose upstreamExpose, property string) bool {
	if expose.Property == property && !isColorModeDeclaration(expose) {
		return true
	}
	for _, child := range expose.Features {
		if exposeHasForeignClaim(child, property) {
			return true
		}
	}
	return false
}

func isColorModeDeclaration(expose upstreamExpose) bool {
	return expose.Type == "enum" && expose.Name == upstreamColorMode
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
