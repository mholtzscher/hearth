// availability.go owns node status interpretation and the per-Entity
// availability reports derived from owned mappings. Availability is advisory:
// it never gates Command dispatch, and one Entity's report never suppresses a
// sibling. Every reason code is fixed and carries no node, endpoint, or Value
// identity.

package zwavejs

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// nodeStatusUnknown, nodeStatusAsleep, nodeStatusAwake, nodeStatusDead, and
// nodeStatusAlive are the Z-Wave JS node status enum. The zero value is
// Unknown, so a node state that omits the field is never mistaken for a live
// node.
const (
	nodeStatusUnknown = 0
	nodeStatusAsleep  = 1
	nodeStatusAwake   = 2
	nodeStatusDead    = 3
	nodeStatusAlive   = 4
)

// Node availability reasons.
const (
	nodeDeadReason          = "adapter.hearth-adapter-zwavejs.node_dead"
	nodeAsleepReason        = "adapter.hearth-adapter-zwavejs.node_asleep"
	nodeNotReadyReason      = "adapter.hearth-adapter-zwavejs.node_not_ready"
	nodeMissingReason       = "adapter.hearth-adapter-zwavejs.node_missing"
	nodeUnknownReason       = "adapter.hearth-adapter-zwavejs.node_unknown"
	capabilityMissingReason = "adapter.hearth-adapter-zwavejs.capability_missing"
)

// bindingKeyNodeID extracts the Z-Wave node ID of one owned Binding key of this
// Adapter: "zwave-<homeID>-node-<nodeID>". It reports false for any other key,
// so a mapping whose node identity cannot be recovered is diagnosed as a
// missing node instead of being silently attached to node zero.
func bindingKeyNodeID(bindingKey string) (int, bool) {
	const separator = "-node-"
	index := strings.LastIndex(bindingKey, separator)
	if index < 0 {
		return 0, false
	}
	nodeText := bindingKey[index+len(separator):]
	if nodeText == "" || !isDecimalDigits(nodeText) {
		return 0, false
	}
	nodeID, err := strconv.Atoi(nodeText)
	if err != nil || nodeID <= 0 {
		return 0, false
	}
	return nodeID, true
}

// nodeAvailability resolves the availability of every Entity of one node from
// the facts of the active connection generation.
//
// The third result reports whether the node has an availability opinion at all:
// a newly discovered node that is still Unknown stays unknown rather than being
// reported available or unavailable.
func nodeAvailability(record *nodeRecord) (adapter.EntityAvailabilityStatus, string, bool) {
	if record == nil || !record.present {
		return adapter.AvailabilityUnavailable, nodeMissingReason, true
	}
	state := record.state
	switch {
	case state.Status == nodeStatusDead:
		return adapter.AvailabilityUnavailable, nodeDeadReason, true
	case state.Status == nodeStatusAsleep:
		return adapter.AvailabilityUnavailable, nodeAsleepReason, true
	case !state.IsListening:
		return adapter.AvailabilityUnavailable, nodeAsleepReason, true
	case !state.Ready || state.InterviewStage != interviewStageComplete:
		return adapter.AvailabilityUnavailable, nodeNotReadyReason, true
	case state.Status != nodeStatusAwake && state.Status != nodeStatusAlive:
		if record.assessed {
			return adapter.AvailabilityUnavailable, nodeUnknownReason, true
		}
		return "", "", false
	default:
		return adapter.AvailabilityAvailable, "", true
	}
}

// availabilityReport resolves one owned mapping to its availability report.
// It reports false only when the mapping's node has no availability opinion at
// all, which is limited to a newly discovered node that is still Unknown.
func (coordinator *runtimeCoordinator) availabilityReport(
	mapping adapter.OwnedMapping,
	observedAt time.Time,
) (adapter.EntityAvailabilityReport, bool) {
	report := adapter.EntityAvailabilityReport{
		EntityID:         mapping.EntityID,
		SourceObservedAt: observedAt.UTC(),
	}
	nodeID, ok := bindingKeyNodeID(mapping.BindingKey)
	if !ok {
		report.Status = adapter.AvailabilityUnavailable
		report.ReasonCode = nodeMissingReason
		return report, true
	}
	record := coordinator.nodes[nodeID]
	status, reason, opinion := nodeAvailability(record)
	if !opinion {
		return adapter.EntityAvailabilityReport{}, false
	}
	if status == adapter.AvailabilityUnavailable {
		report.Status = adapter.AvailabilityUnavailable
		report.ReasonCode = reason
		return report, true
	}
	if !coordinator.nodeProvides(nodeID, mapping.EntityID) {
		report.Status = adapter.AvailabilityUnavailable
		report.ReasonCode = capabilityMissingReason
		return report, true
	}
	report.Status = adapter.AvailabilityAvailable
	return report, true
}

// availabilityReports resolves every owned mapping in owned-mapping order. An
// Entity without an opinion is omitted, so the returned slice is never longer
// than the owned mapping set.
func (coordinator *runtimeCoordinator) availabilityReports(
	observedAt time.Time,
) []adapter.EntityAvailabilityReport {
	reports := make([]adapter.EntityAvailabilityReport, 0, len(coordinator.order))
	for _, key := range coordinator.order {
		report, ok := coordinator.availabilityReport(coordinator.mappings[key], observedAt)
		if !ok {
			continue
		}
		reports = append(reports, report)
	}
	return reports
}

// nodeAvailabilityReports resolves only the owned mappings of one node, in
// owned-mapping order. A topology Event refreshes one node, so it reports only
// that node's Entities.
func (coordinator *runtimeCoordinator) nodeAvailabilityReports(
	nodeID int,
	observedAt time.Time,
) []adapter.EntityAvailabilityReport {
	reports := make([]adapter.EntityAvailabilityReport, 0)
	for _, key := range coordinator.order {
		mapping := coordinator.mappings[key]
		mappingNodeID, ok := bindingKeyNodeID(mapping.BindingKey)
		if !ok || mappingNodeID != nodeID {
			continue
		}
		report, reportable := coordinator.availabilityReport(mapping, observedAt)
		if !reportable {
			continue
		}
		reports = append(reports, report)
	}
	return reports
}

// nodeProvides reports whether the active route snapshot of one node still
// serves one canonical Entity.
func (coordinator *runtimeCoordinator) nodeProvides(nodeID int, entityID string) bool {
	record := coordinator.nodes[nodeID]
	if record == nil {
		return false
	}
	for _, route := range record.routes {
		if route.EntityID == entityID {
			return true
		}
	}
	return false
}

// reportAvailability sends availability reports in batches of at most 256.
// An empty batch is not a report: the SDK rejects an empty availability batch.
func (zwave *Adapter) reportAvailability(
	ctx context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	for len(reports) > 0 {
		count := min(len(reports), availabilityPage)
		if err := zwave.session.ReportEntityAvailability(ctx, reports[:count]); err != nil {
			return &sessionOperationError{
				operation: "report Z-Wave Entity availability",
				err:       err,
			}
		}
		reports = reports[count:]
	}
	return nil
}
