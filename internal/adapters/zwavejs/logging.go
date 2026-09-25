package zwavejs

import (
	"context"
	"errors"
	"log/slog"
)

// adapterComponent is the only component value emitted from this package. It is
// attached once in newAdapter so every record carries a bounded subsystem label
// without repeating app or pid, which belong to the root application logger.
const adapterComponent = "zwavejs"

// eventKey is the shared slog attribute key carrying the stable dotted event
// name on every record emitted from this package. Event values stay whole
// literals at each emission site so searching a value finds its site.
const eventKey = "event"

// Fixed diagnostic classifications. A code never carries a driver version,
// Home ID, node identity, Value ID, payload, or upstream error text.
const (
	codeIncompatibleProtocol     = "incompatible_protocol"
	codeInvalidSnapshot          = "invalid_snapshot"
	codeNetworkIdentityMismatch  = "network_identity_mismatch"
	codeSessionOperationFailed   = "session_operation_failed"
	codeUpstreamConnectionFailed = "upstream_connection_failed"
	codeAmbiguousSetValue        = "ambiguous_set_value"
	codeLinkedPublishFailed      = "linked_publish_failed"
	codeStateUnrepresentable     = "state_unrepresentable"
)

// isIncompatibleSchema reports a server whose schema range excludes schema 29.
func isIncompatibleSchema(err error) bool {
	_, found := errors.AsType[*incompatibleSchemaVersionError](err)
	return found
}

// isInvalidSnapshot reports a version frame or snapshot the client cannot
// consume, including a version/snapshot Home ID disagreement.
func isInvalidSnapshot(err error) bool {
	if _, found := errors.AsType[*malformedVersionFrameError](err); found {
		return true
	}
	if _, found := errors.AsType[*malformedSnapshotError](err); found {
		return true
	}
	_, found := errors.AsType[*homeIDMismatchError](err)
	return found
}

// isNetworkIdentityMismatch reports owned mappings that do not agree with one
// another and the connected network, or an owned key that is not a Z-Wave node
// Binding at all.
func isNetworkIdentityMismatch(err error) bool {
	if _, found := errors.AsType[*networkIdentityMismatchError](err); found {
		return true
	}
	_, found := errors.AsType[*ownedMappingIdentityError](err)
	return found
}

// isUpstreamRejection reports a correlated result envelope the server refused.
func isUpstreamRejection(err error) bool {
	_, found := errors.AsType[*upstreamRejectionError](err)
	return found
}

// isSetValueRefused reports a node.set_value result that is not one of the
// documented successful statuses.
func isSetValueRefused(err error) bool {
	_, found := errors.AsType[*setValueRefusedError](err)
	return found
}

// isRequestTimeout reports a correlated request whose result did not arrive
// before its context ended. The client ends the generation because a late
// result could no longer be routed.
func isRequestTimeout(err error) bool {
	_, found := errors.AsType[*requestTimeoutError](err)
	return found
}

// isSessionOperationFailed reports a failure of one SDK Session call. Such a
// failure is terminal: Run returns it rather than reconnecting.
func isSessionOperationFailed(err error) bool {
	_, found := errors.AsType[*sessionOperationError](err)
	return found
}

// isContextCancellation reports an operation that stopped because its context
// ended: its generation was torn down, or the runtime is shutting down. Such a
// completion is expected teardown, not a Session failure, so it never terminates
// the runtime.
func isContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// unhealthyReason maps one connection-generation failure to its fixed unhealthy
// reason code. Every upstream failure that is not a protocol, snapshot, or
// identity fault is reported as an unavailable external system.
func unhealthyReason(err error) string {
	switch {
	case isIncompatibleSchema(err):
		return incompatibleProtocolReason
	case isInvalidSnapshot(err):
		return invalidSnapshotReason
	case isNetworkIdentityMismatch(err):
		return networkIdentityMismatchReason
	default:
		return externalSystemUnavailableReason
	}
}

// zwaveJSErrorCode maps one retry-loop failure to a fixed diagnostic
// classification for the dependency.retrying record.
func zwaveJSErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case isIncompatibleSchema(err):
		return codeIncompatibleProtocol
	case isInvalidSnapshot(err):
		return codeInvalidSnapshot
	case isNetworkIdentityMismatch(err):
		return codeNetworkIdentityMismatch
	case isSessionOperationFailed(err):
		return codeSessionOperationFailed
	default:
		return codeUpstreamConnectionFailed
	}
}

// logReconcileCompleted summarizes one activated connection generation. Counts
// are already available reconciliation results; a zero Entity count is explicit
// so an empty network is distinguishable from a failed reconcile.
func (zwave *Adapter) logReconcileCompleted(ctx context.Context, entityCount, isolated int) {
	zwave.logger.InfoContext(
		ctx,
		"Z-Wave JS reconciliation completed",
		slog.String(eventKey, "adapter.reconcile_completed"),
		slog.Int("entity_count", entityCount),
		slog.Int("isolated_node_count", isolated),
	)
}

// logIsolatedNode records one node left out of the activated routes. Only the
// fixed reason classification is kept: node names, locations, labels, product
// fingerprints, and Value IDs are never logged.
func (zwave *Adapter) logIsolatedNode(ctx context.Context, reasonCode string) {
	zwave.logger.DebugContext(
		ctx,
		"isolated Z-Wave node",
		slog.String(eventKey, "adapter.node_isolated"),
		slog.String("reason_code", reasonCode),
	)
}
