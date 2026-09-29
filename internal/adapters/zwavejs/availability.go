// availability.go owns node status interpretation and per-Entity availability reports. Availability never gates Command
// dispatch or suppresses sibling reports. Fixed reason codes carry no node, endpoint, or Value identity.

package zwavejs

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// nodeStatusUnknown, nodeStatusAsleep, nodeStatusAwake, nodeStatusDead, and nodeStatusAlive are the Z-Wave JS node
// status enum. Zero means Unknown, so an omitted status cannot imply a live node.
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

// bindingKeyNodeID extracts the positive node ID after the last "-node-" in an owned Binding key. Other keys report
// false, so a mapping without a recoverable node identity never attaches to node zero.
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

// nodeAvailability resolves an Entity's node availability from the complete start_listening snapshot for its active
// connection generation.
//
// The third result is false for a newly discovered node still marked Unknown.
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

// availabilityReport resolves one owned mapping to its availability report. It reports false when a newly discovered
// node is still Unknown.
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

// availabilityReports follows owned-mapping order and omits Entities without an availability opinion.
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

// nodeProvides checks whether the active node routes serve the Entity.
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

// reportAvailability sends batches of at most 256 reports. The SDK rejects empty batches.
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
