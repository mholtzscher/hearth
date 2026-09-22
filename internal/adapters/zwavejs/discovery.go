// discovery.go owns snapshot planning: Z-Wave node eligibility, endpoint
// capability validation, the stable network/node/endpoint identity shared by
// Binding keys and external IDs, and the one registration a node produces. It
// is pure: planning reads a snapshot and returns either a complete registration
// with typed Entity plans or a stable rejection code, and it performs no I/O.

package zwavejs

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// deviceKindLight and deviceKindRelay are Hearth's Device kinds. A node is a
	// light when any planned Entity is brightness, and a relay otherwise.
	deviceKindLight = "light"
	deviceKindRelay = "relay"
)

// nodeRejectionCode is the stable diagnostic code of one unplanned node. It is
// an internal diagnostic: it never becomes a Hearth reason code on its own.
type nodeRejectionCode string

const (
	// rejectionNodeMalformed marks a node without a usable node ID.
	rejectionNodeMalformed nodeRejectionCode = "malformed_node"
	// rejectionNodeController marks the network controller, which is never an
	// actuator.
	rejectionNodeController nodeRejectionCode = "controller_node"
	// rejectionNodeSleeping marks a node that is not always listening. v1
	// excludes sleeping and frequently listening actuators because a write could
	// be queued past the Command deadline.
	rejectionNodeSleeping nodeRejectionCode = "node_not_listening"
	// rejectionNodeNotReady marks a node that is not ready.
	rejectionNodeNotReady nodeRejectionCode = "node_not_ready"
	// rejectionNodeInterview marks a node whose interview is incomplete.
	rejectionNodeInterview nodeRejectionCode = "interview_incomplete"
	// rejectionNodeNoCapability marks a node with no valid Binary Switch or
	// Multilevel Switch pair on any endpoint.
	rejectionNodeNoCapability nodeRejectionCode = "no_eligible_endpoint"
	// rejectionNodeTooManyEntities marks a node above Hearth's 64 Entity bound.
	rejectionNodeTooManyEntities nodeRejectionCode = "too_many_entities"
	// rejectionNodeInvalidDescriptor marks a node whose names or plans cannot be
	// registered.
	rejectionNodeInvalidDescriptor nodeRejectionCode = "invalid_descriptor"
	// rejectionNodeDuplicateNodeID marks a node ID reported more than once in one
	// snapshot. The identity is ambiguous, so every copy is isolated.
	rejectionNodeDuplicateNodeID nodeRejectionCode = "duplicate_node_id"
)

// discoveredNode is one eligible Z-Wave node: its stable identity, the one
// registration that covers every endpoint it provides, and the typed Entity
// plans in registration order.
type discoveredNode struct {
	HomeID       string
	NodeID       int
	Registration adapter.Registration
	Plans        []entityPlan
}

// nodeRejection is one node this Adapter does not plan, with a safe diagnostic
// identity that contains no household metadata.
type nodeRejection struct {
	HomeID string
	NodeID int
	Code   nodeRejectionCode
}

// networkPlan is the complete planning result for one start-listening snapshot.
// Nodes keep snapshot order; rejections are diagnostics only.
type networkPlan struct {
	HomeID     string
	Nodes      []discoveredNode
	Rejections []nodeRejection
}

// plannedProperty identifies one accepted state or command target Value
// property.
type plannedProperty uint8

const (
	// plannedPropertyCurrent is the Value a planned Entity reads as State.
	plannedPropertyCurrent plannedProperty = iota + 1
	// plannedPropertyTarget is the Value a planned Entity writes Commands to.
	plannedPropertyTarget
)

// valueSlot identifies one accepted current or target Value of one endpoint.
type valueSlot uint8

const (
	slotBinaryCurrent valueSlot = iota + 1
	slotBinaryTarget
	slotMultilevelCurrent
	slotMultilevelTarget
)

// maximumEndpointValues is the number of accepted Value slots one endpoint can
// report: a current and a target for each of the two planned Command Classes.
const maximumEndpointValues = 4

// maximumEndpointCapabilities is the number of Entity capabilities one endpoint
// can provide: power and brightness.
const maximumEndpointCapabilities = 2

// endpointCandidates holds one endpoint's structurally valid Value candidates. A
// repeated Value ID for one slot clears that slot only: the slot can no longer be
// routed unambiguously, so the capability that depends on it is isolated while
// every sibling capability of the endpoint stays plannable.
type endpointCandidates struct {
	binaryCurrent     *valueState
	binaryTarget      *valueState
	multilevelCurrent *valueState
	multilevelTarget  *valueState
}

// networkIdentityMismatchError reports that this Adapter's owned mappings do not
// agree with one another and the connected Z-Wave network. The Adapter reports
// unhealthy with adapter.hearth-adapter-zwavejs.network_identity_mismatch,
// registers nothing, and publishes no Observation until the operator restores
// the intended Z-Wave JS UI target or configures a new Adapter ID for the other
// network.
type networkIdentityMismatchError struct {
	// OwnedHomeIDs is the sorted set of distinct network identities found in the
	// owned Binding keys.
	OwnedHomeIDs []string
	// ConnectedHomeID is the normalized Home ID of the connected network.
	ConnectedHomeID string
}

func (err *networkIdentityMismatchError) Error() string {
	return "zwavejs: owned mappings carry network identity " +
		strings.Join(err.OwnedHomeIDs, ",") + " but the connected network is " +
		err.ConnectedHomeID
}

// ownedMappingIdentityError reports one owned Binding key that does not carry a
// Z-Wave node Binding identity.
type ownedMappingIdentityError struct {
	BindingKey string
}

func (err *ownedMappingIdentityError) Error() string {
	return "zwavejs: owned mapping Binding key " + err.BindingKey + " is not a Z-Wave node Binding"
}

// normalizedHomeID renders one Z-Wave Home ID as the lowercase, zero-padded
// eight-digit hexadecimal network identity that every Binding key, Device
// external ID, and Entity external ID carries.
func normalizedHomeID(homeID uint32) string {
	return fmt.Sprintf("%08x", homeID)
}

// nodeBindingKey is the stable Hearth Binding key of one Z-Wave node slot:
// "zwave-<homeID>-node-<nodeID>". Node names, locations, labels, product
// fingerprints, and driver versions never enter it.
func nodeBindingKey(homeID string, nodeID int) string {
	return "zwave-" + homeID + "-node-" + strconv.Itoa(nodeID)
}

// nodeDeviceExternalID is the stable Hearth Device external ID of one Z-Wave
// node slot: "<homeID>/node/<nodeID>".
func nodeDeviceExternalID(homeID string, nodeID int) string {
	return homeID + "/node/" + strconv.Itoa(nodeID)
}

// nodeEntityExternalID is the stable Hearth Entity external ID of one endpoint
// capability: "<homeID>/node/<nodeID>/ep/<endpoint>/<kind>".
func nodeEntityExternalID(homeID string, nodeID, endpoint int, kind entityKind) string {
	return nodeDeviceExternalID(homeID, nodeID) + "/ep/" + strconv.Itoa(endpoint) + "/" + kind.slug()
}

// endpointEntityKey is the Hearth Entity key of one endpoint capability. The
// root endpoint keeps the bare kind slug.
func endpointEntityKey(kind entityKind, endpoint int) string {
	if endpoint == 0 {
		return kind.slug()
	}
	return kind.slug() + "-ep" + strconv.Itoa(endpoint)
}

// entityMetadata builds the stable Hearth Entity identity of one endpoint
// capability: key, external ID, and display name.
func entityMetadata(
	homeID string,
	nodeID, endpoint int,
	kind entityKind,
	label string,
) adapter.EntityMetadata {
	return adapter.EntityMetadata{
		Key:        endpointEntityKey(kind, endpoint),
		ExternalID: nodeEntityExternalID(homeID, nodeID, endpoint, kind),
		Name:       endpointEntityName(kind, endpoint, label),
	}
}

// endpointEntityName is the display name of one endpoint capability. A valid
// endpoint label prefixes the capability name, and every other endpoint falls
// back to "Endpoint <endpoint>". Root Entities use the bare capability name.
func endpointEntityName(kind entityKind, endpoint int, label string) string {
	base := kind.displayName()
	if endpoint == 0 {
		return base
	}
	return endpointNamePrefix(endpoint, label, base) + " " + base
}

// endpointNamePrefix resolves the display prefix of one endpoint. A label that is
// empty, only decimal digits, or too long to keep the composed name within
// Hearth's 1..128 rune bound is not a valid prefix, so the deterministic
// "Endpoint <endpoint>" fallback is used instead.
func endpointNamePrefix(endpoint int, label, base string) string {
	trimmed := strings.TrimSpace(label)
	fallback := "Endpoint " + strconv.Itoa(endpoint)
	if trimmed == "" || isDecimalDigits(trimmed) {
		return fallback
	}
	if utf8.RuneCountInString(trimmed)+1+utf8.RuneCountInString(base) > maximumDescriptorRunes {
		return fallback
	}
	return trimmed
}

// isDecimalDigits reports whether a label consists only of decimal digits. An
// empty label reports true; callers check for emptiness first.
func isDecimalDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// nodeDeviceName is the Device display name of one Z-Wave node: the trimmed node
// name, then the trimmed node label, then "Z-Wave Node <nodeID>".
func nodeDeviceName(node nodeState) string {
	if trimmed := strings.TrimSpace(node.Name); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(node.Label); trimmed != "" {
		return trimmed
	}
	return "Z-Wave Node " + strconv.Itoa(node.NodeID)
}

// validDescriptorName reports whether one display name satisfies Hearth's 1..128
// rune bound. A longer name is rejected, never truncated.
func validDescriptorName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= maximumDescriptorRunes
}

// bindingKeyNetworkIdentity extracts the normalized network identity from one
// Hearth Binding key of this Adapter: "zwave-<homeID>-node-<nodeID>". It reports
// false for any other key, so a mapping owned under a different shape is
// diagnosed instead of silently ignored.
func bindingKeyNetworkIdentity(bindingKey string) (string, bool) {
	const (
		prefix    = "zwave-"
		separator = "-node-"
	)
	if !strings.HasPrefix(bindingKey, prefix) {
		return "", false
	}
	homeID, nodeText, found := strings.Cut(bindingKey[len(prefix):], separator)
	if !found || len(homeID) != 8 || nodeText == "" || !isDecimalDigits(nodeText) {
		return "", false
	}
	for index := range len(homeID) {
		character := homeID[index]
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return "", false
	}
	return homeID, true
}

// verifyNetworkIdentity compares the single network identity recorded in this
// Adapter's owned mappings with the connected Home ID. Existing mappings for a
// different network mean one Adapter ID was repointed at another controller, so
// v1 registers nothing rather than reusing node-number-shaped identity.
func verifyNetworkIdentity(mappings []adapter.OwnedMapping, connectedHomeID uint32) error {
	connected := normalizedHomeID(connectedHomeID)
	owned := make(map[string]struct{})
	for _, mapping := range mappings {
		identity, ok := bindingKeyNetworkIdentity(mapping.BindingKey)
		if !ok {
			return &ownedMappingIdentityError{BindingKey: mapping.BindingKey}
		}
		owned[identity] = struct{}{}
	}
	if len(owned) == 0 {
		return nil
	}
	if len(owned) == 1 {
		if _, matches := owned[connected]; matches {
			return nil
		}
	}
	return &networkIdentityMismatchError{
		OwnedHomeIDs:    slices.Sorted(maps.Keys(owned)),
		ConnectedHomeID: connected,
	}
}

// planNetwork plans every eligible node of one complete start-listening snapshot
// whose network identity is the version frame's Home ID. A malformed node is
// isolated into a rejection; one bad node never discards its siblings.
func planNetwork(homeID uint32, snapshot networkSnapshot) networkPlan {
	home := normalizedHomeID(homeID)
	nodes := snapshot.State.Nodes
	plan := networkPlan{
		HomeID:     home,
		Nodes:      make([]discoveredNode, 0, len(nodes)),
		Rejections: make([]nodeRejection, 0),
	}
	counts := make(map[int]int, len(nodes))
	for _, node := range nodes {
		counts[node.NodeID]++
	}
	for _, node := range nodes {
		switch {
		case node.NodeID <= 0:
			plan.Rejections = append(plan.Rejections, nodeRejection{
				HomeID: home,
				NodeID: node.NodeID,
				Code:   rejectionNodeMalformed,
			})
		case counts[node.NodeID] > 1:
			plan.Rejections = append(plan.Rejections, nodeRejection{
				HomeID: home,
				NodeID: node.NodeID,
				Code:   rejectionNodeDuplicateNodeID,
			})
		default:
			discovered, rejection := planNode(home, node)
			if rejection != nil {
				plan.Rejections = append(plan.Rejections, *rejection)
				continue
			}
			plan.Nodes = append(plan.Nodes, discovered)
		}
	}
	return plan
}

// nodeIneligibleReason returns the stable code of the first node eligibility rule
// a node fails: not the controller, always listening, ready, and interview
// Complete. It reports false when the node may be planned.
func nodeIneligibleReason(node nodeState) (nodeRejectionCode, bool) {
	switch {
	case node.NodeID <= 0:
		return rejectionNodeMalformed, true
	case node.IsController:
		return rejectionNodeController, true
	case !node.IsListening:
		return rejectionNodeSleeping, true
	case !node.Ready:
		return rejectionNodeNotReady, true
	case node.InterviewStage != interviewStageComplete:
		return rejectionNodeInterview, true
	default:
		return "", false
	}
}

// planNodeState plans one refreshed node after a topology, ready, or metadata
// Event. It is the single-node reconciliation entry point and applies exactly the
// same eligibility, endpoint, identity, and Entity-bound rules as the snapshot
// planner, so a refresh can never diverge from startup.
func planNodeState(homeID uint32, node nodeState) (discoveredNode, *nodeRejection) {
	return planNode(normalizedHomeID(homeID), node)
}

// planNode builds the one registration and the typed Entity plans of a single
// Z-Wave node. It returns a rejection instead of an error so one unplannable
// node can never discard its siblings.
func planNode(home string, node nodeState) (discoveredNode, *nodeRejection) {
	if code, ineligible := nodeIneligibleReason(node); ineligible {
		return discoveredNode{}, rejectedNode(home, node.NodeID, code)
	}
	name := nodeDeviceName(node)
	if !validDescriptorName(name) {
		return discoveredNode{}, rejectedNode(home, node.NodeID, rejectionNodeInvalidDescriptor)
	}
	plans, err := planNodeEndpoints(home, node)
	switch {
	case err != nil:
		return discoveredNode{}, rejectedNode(home, node.NodeID, rejectionNodeInvalidDescriptor)
	case len(plans) == 0:
		return discoveredNode{}, rejectedNode(home, node.NodeID, rejectionNodeNoCapability)
	case len(plans) > maximumPlannedEntitiesPerNode:
		return discoveredNode{}, rejectedNode(home, node.NodeID, rejectionNodeTooManyEntities)
	}
	if err = validateEntityPlans(plans); err != nil {
		return discoveredNode{}, rejectedNode(home, node.NodeID, rejectionNodeInvalidDescriptor)
	}
	return newDiscoveredNode(home, node.NodeID, name, plans), nil
}

// rejectedNode builds one diagnostic rejection.
func rejectedNode(home string, nodeID int, code nodeRejectionCode) *nodeRejection {
	return &nodeRejection{HomeID: home, NodeID: nodeID, Code: code}
}

// newDiscoveredNode assembles the one registration of one eligible node.
func newDiscoveredNode(
	home string,
	nodeID int,
	name string,
	plans []entityPlan,
) discoveredNode {
	descriptors := make([]adapter.EntityDescriptor, 0, len(plans))
	kind := deviceKindRelay
	for _, plan := range plans {
		descriptors = append(descriptors, plan.Descriptor)
		if plan.Kind == entityKindBrightness {
			kind = deviceKindLight
		}
	}
	externalID := nodeDeviceExternalID(home, nodeID)
	return discoveredNode{
		HomeID: home,
		NodeID: nodeID,
		Plans:  plans,
		Registration: adapter.Registration{
			BindingKey: nodeBindingKey(home, nodeID),
			Device: adapter.DeviceDescriptor{
				ExternalID: &externalID,
				Name:       name,
				Kind:       kind,
			},
			Entities: descriptors,
		},
	}
}

// planNodeEndpoints plans every endpoint of one eligible node in ascending
// endpoint order, power before brightness per endpoint.
func planNodeEndpoints(home string, node nodeState) ([]entityPlan, error) {
	labels, isolated := endpointLabels(node)
	plans := make([]entityPlan, 0, maximumEndpointCapabilities)
	for _, endpoint := range candidateEndpoints(node.Values) {
		if _, ambiguous := isolated[endpoint]; ambiguous {
			continue
		}
		candidates := collectEndpointCandidates(node.Values, endpoint)
		endpointPlans, err := planEndpointCapabilities(
			home,
			node.NodeID,
			endpoint,
			labels[endpoint],
			candidates,
		)
		if err != nil {
			return nil, err
		}
		plans = append(plans, endpointPlans...)
	}
	return plans, nil
}

// planEndpointCapabilities builds the power and brightness plans of one endpoint
// in registration order: power first, brightness second. A valid Binary Switch
// pair owns power; otherwise a valid Multilevel Switch pair derives power from
// its own current Value.
func planEndpointCapabilities(
	home string,
	nodeID, endpoint int,
	label string,
	candidates endpointCandidates,
) ([]entityPlan, error) {
	binaryPower := candidates.validBinaryPair()
	multilevel := candidates.validMultilevelPair()
	plans := make([]entityPlan, 0, maximumEndpointCapabilities)
	if binaryPower {
		power, err := newPowerEntityPlan(powerPlanInput{
			HomeID: home, NodeID: nodeID, Endpoint: endpoint, Label: label,
			Current:       candidates.binaryCurrent,
			Target:        candidates.binaryTarget,
			DecodeCurrent: decodeBinaryPowerState,
			EncodeValue:   encodePowerState,
		})
		if err != nil {
			return nil, err
		}
		plans = append(plans, power)
	}
	if !multilevel {
		return plans, nil
	}
	if !binaryPower {
		power, err := newPowerEntityPlan(powerPlanInput{
			HomeID: home, NodeID: nodeID, Endpoint: endpoint, Label: label,
			Current:        candidates.multilevelCurrent,
			Target:         candidates.multilevelTarget,
			FromMultilevel: true,
			DecodeCurrent:  decodeMultilevelPowerState,
			EncodeValue:    encodeMultilevelPowerValue,
		})
		if err != nil {
			return nil, err
		}
		plans = append(plans, power)
	}
	brightness, err := newBrightnessEntityPlan(brightnessPlanInput{
		HomeID: home, NodeID: nodeID, Endpoint: endpoint, Label: label,
		Current: candidates.multilevelCurrent,
		Target:  candidates.multilevelTarget,
	})
	if err != nil {
		return nil, err
	}
	return append(plans, brightness), nil
}

// endpointLabels indexes the endpoint labels of one node. An endpoint index
// repeated in the node inventory is structurally ambiguous, so it is isolated
// instead of guessed: it produces no plans while every other endpoint stays
// plannable.
func endpointLabels(node nodeState) (map[int]string, map[int]struct{}) {
	labels := make(map[int]string, len(node.Endpoints))
	isolated := make(map[int]struct{})
	for _, endpoint := range node.Endpoints {
		if _, duplicate := labels[endpoint.Index]; duplicate {
			isolated[endpoint.Index] = struct{}{}
			continue
		}
		labels[endpoint.Index] = endpoint.EndpointLabel
	}
	return labels, isolated
}

// candidateEndpoints returns every endpoint that reports at least one Value, in
// ascending endpoint order. A negative endpoint index is not a Value ID this
// client can address: it would plan and register an Entity whose Commands the
// client refuses, so it is isolated during planning instead of poisoning a
// sibling endpoint.
func candidateEndpoints(values []valueState) []int {
	endpoints := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value.Endpoint < 0 {
			continue
		}
		endpoints[value.Endpoint] = struct{}{}
	}
	return slices.Sorted(maps.Keys(endpoints))
}

// collectEndpointCandidates gathers one endpoint's candidates. A repeated Value
// ID for one slot clears that slot, because its Value ID set can no longer be
// routed unambiguously. Only the capability that reads the cleared slot is
// isolated: a duplicate Binary Switch pair cannot suppress a valid Multilevel
// Switch brightness or derived power on the same endpoint, and a duplicate
// Multilevel Switch pair cannot suppress a valid Binary Switch power.
func collectEndpointCandidates(values []valueState, endpoint int) endpointCandidates {
	candidates := endpointCandidates{}
	seen := make(map[valueSlot]struct{}, maximumEndpointValues)
	for index := range values {
		slot, ok := plannedValueSlot(values[index], endpoint)
		if !ok {
			continue
		}
		if _, duplicate := seen[slot]; duplicate {
			candidates.clear(slot)
			continue
		}
		seen[slot] = struct{}{}
		candidates.assign(slot, &values[index])
	}
	return candidates
}

// assign records one candidate Value in its slot.
func (candidates *endpointCandidates) assign(slot valueSlot, value *valueState) {
	switch slot {
	case slotBinaryCurrent:
		candidates.binaryCurrent = value
	case slotBinaryTarget:
		candidates.binaryTarget = value
	case slotMultilevelCurrent:
		candidates.multilevelCurrent = value
	case slotMultilevelTarget:
		candidates.multilevelTarget = value
	}
}

// clear removes one slot's candidate because its Value ID was reported more than
// once on the endpoint and can no longer be routed unambiguously.
func (candidates *endpointCandidates) clear(slot valueSlot) {
	switch slot {
	case slotBinaryCurrent:
		candidates.binaryCurrent = nil
	case slotBinaryTarget:
		candidates.binaryTarget = nil
	case slotMultilevelCurrent:
		candidates.multilevelCurrent = nil
	case slotMultilevelTarget:
		candidates.multilevelTarget = nil
	}
}

// validBinaryPair reports whether one endpoint's Binary Switch pair can plan
// power: one readable boolean currentValue and one writeable boolean
// targetValue.
func (candidates *endpointCandidates) validBinaryPair() bool {
	return validBooleanCurrentValue(candidates.binaryCurrent) &&
		validBooleanTargetValue(candidates.binaryTarget)
}

// validMultilevelPair reports whether one endpoint's Multilevel Switch pair can
// plan brightness: one readable numeric currentValue with bounds exactly 0..99
// and one writeable numeric targetValue.
func (candidates *endpointCandidates) validMultilevelPair() bool {
	return validLevelCurrentValue(candidates.multilevelCurrent) &&
		validLevelTargetValue(candidates.multilevelTarget)
}

// validBooleanCurrentValue requires valid boolean metadata and readable
// support.
func validBooleanCurrentValue(value *valueState) bool {
	return value != nil && value.Metadata.Valid &&
		value.Metadata.Type == metadataTypeBoolean && value.Metadata.Readable
}

// validBooleanTargetValue requires valid boolean metadata and writeable
// support. The type must come from metadata, because a target may report no
// current value from which a type could be inferred.
func validBooleanTargetValue(value *valueState) bool {
	return value != nil && value.Metadata.Valid &&
		value.Metadata.Type == metadataTypeBoolean && value.Metadata.Writeable
}

// validLevelCurrentValue requires valid number metadata, readable support, and
// bounds exactly 0..99. A level scaled to another range cannot round-trip a
// Hearth brightness State, so such a Value is not a candidate.
func validLevelCurrentValue(value *valueState) bool {
	if value == nil || !value.Metadata.Valid || value.Metadata.Type != metadataTypeNumber ||
		!value.Metadata.Readable {
		return false
	}
	if value.Metadata.Min == nil || value.Metadata.Max == nil {
		return false
	}
	if !isFiniteFloat(*value.Metadata.Min) || !isFiniteFloat(*value.Metadata.Max) {
		return false
	}
	return *value.Metadata.Min == 0 && *value.Metadata.Max == zWaveLevelMaximum
}

// validLevelTargetValue requires valid number metadata and writeable support.
func validLevelTargetValue(value *valueState) bool {
	return value != nil && value.Metadata.Valid &&
		value.Metadata.Type == metadataTypeNumber && value.Metadata.Writeable
}

// plannedValueSlot maps one snapshot Value to the slot this Adapter plans, and
// false for any Value that is not a candidate. A non-candidate never becomes a
// plan and never isolates a sibling capability.
func plannedValueSlot(value valueState, endpoint int) (valueSlot, bool) {
	if value.Endpoint != endpoint || value.Endpoint < 0 {
		return 0, false
	}
	property, ok := plannedValueProperty(value.valueID)
	if !ok {
		return 0, false
	}
	switch value.CommandClass {
	case commandClassBinarySwitch:
		if property == plannedPropertyCurrent {
			return slotBinaryCurrent, true
		}
		return slotBinaryTarget, true
	case commandClassMultilevelSwitch:
		if property == plannedPropertyCurrent {
			return slotMultilevelCurrent, true
		}
		return slotMultilevelTarget, true
	default:
		return 0, false
	}
}

// plannedValueProperty maps one Value ID property to the property v1 plans, and
// false for every other property, a numeric property name, an invalid (null)
// property, or a propertyKey. A keyed Value is a different Value than the
// unkeyed one, so it is never a candidate.
func plannedValueProperty(id valueID) (plannedProperty, bool) {
	if id.Property.Numeric || id.Property.Invalid || id.Property.Name == "" ||
		valueIDHasPropertyKey(id) {
		return 0, false
	}
	switch id.Property.Name {
	case valuePropertyCurrentValue:
		return plannedPropertyCurrent, true
	case valuePropertyTargetValue:
		return plannedPropertyTarget, true
	default:
		return 0, false
	}
}

// valueIDHasPropertyKey reports whether one Value ID carries a property key. An
// explicit JSON null key counts as absent.
func valueIDHasPropertyKey(id valueID) bool {
	trimmed := bytes.TrimSpace(id.PropertyKey)
	return len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null"))
}
