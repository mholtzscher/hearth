package zwavejs

import (
	"context"
	"errors"
	"log/slog"
)

// adapterComponent labels every package record once in newAdapter. The root logger supplies app and pid.
const adapterComponent = "zwavejs"

// eventKey carries stable dotted event names. Each emission uses a whole literal so a search for its value finds the
// site.
const eventKey = "event"

// Fixed diagnostic classifications. A code never carries a driver version, Home ID, node identity, Value ID, payload,
// or upstream error text.
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

// isIncompatibleSchema detects a server whose schema range excludes schema 29.
func isIncompatibleSchema(err error) bool {
	_, found := errors.AsType[*incompatibleSchemaVersionError](err)
	return found
}

// isInvalidSnapshot detects a version frame or snapshot the client cannot consume, including a Home ID disagreement
// between them.
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

// isNetworkIdentityMismatch detects conflicting owned mappings or a key that is not a Z-Wave node Binding.
func isNetworkIdentityMismatch(err error) bool {
	if _, found := errors.AsType[*networkIdentityMismatchError](err); found {
		return true
	}
	_, found := errors.AsType[*ownedMappingIdentityError](err)
	return found
}

// isUpstreamRejection detects a refused correlated result envelope.
func isUpstreamRejection(err error) bool {
	_, found := errors.AsType[*upstreamRejectionError](err)
	return found
}

// isSetValueRefused detects a node.set_value result without a documented successful status.
func isSetValueRefused(err error) bool {
	_, found := errors.AsType[*setValueRefusedError](err)
	return found
}

// isRequestTimeout detects a request whose context ended before its result arrived. The client ends the generation
// because it cannot route a late result.
func isRequestTimeout(err error) bool {
	_, found := errors.AsType[*requestTimeoutError](err)
	return found
}

// isSessionOperationFailed detects a terminal SDK Session failure. Run returns it without reconnecting.
func isSessionOperationFailed(err error) bool {
	_, found := errors.AsType[*sessionOperationError](err)
	return found
}

// isContextCancellation detects generation teardown or runtime shutdown. Expected teardown is not a Session failure and
// does not terminate the runtime.
func isContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// unhealthyReason maps generation failures to fixed codes. Other upstream failures report an unavailable external
// system.
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

// zwaveJSErrorCode classifies retry failures for dependency.retrying records.
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

// logReconcileCompleted records reconciliation counts. An explicit zero Entity count distinguishes an empty network
// from a failed reconcile.
func (zwave *Adapter) logReconcileCompleted(ctx context.Context, entityCount, isolated int) {
	zwave.logger.InfoContext(
		ctx,
		"Z-Wave JS reconciliation completed",
		slog.String(eventKey, "adapter.reconcile_completed"),
		slog.Int("entity_count", entityCount),
		slog.Int("isolated_node_count", isolated),
	)
}

// logIsolatedNode records only a fixed reason code. It never logs node names, locations, labels, product fingerprints,
// or Value IDs.
func (zwave *Adapter) logIsolatedNode(ctx context.Context, reasonCode string) {
	zwave.logger.DebugContext(
		ctx,
		"isolated Z-Wave node",
		slog.String(eventKey, "adapter.node_isolated"),
		slog.String("reason_code", reasonCode),
	)
}
