package ecowitt

import (
	"context"
	"errors"
	"log/slog"
)

// adapterComponent is the only component value emitted from this package. It
// is attached once in the Adapter constructor so every record carries a
// bounded subsystem label without repeating the root application's app or pid
// attributes.
const adapterComponent = "ecowitt"

// eventKey is the shared slog attribute key carrying the stable dotted event
// name on every record emitted from this package. Event values stay whole
// literals at each emission site so searching the value finds its site.
const eventKey = "event"

// errorCodeKey is the shared slog attribute key carrying a fixed diagnostic
// classification. Codes are closed literals: a code never carries a topic, a
// payload, a field value, a station identity, or secret-file content.
const errorCodeKey = "error_code"

// Ignored-report classifications. Every ignored report logs exactly one of
// these with the payload byte count, never the topic or payload.
const (
	retainedReportIgnoredCode    = "retained_report"
	unexpectedTopicIgnoredCode   = "unexpected_topic"
	endedGenerationIgnoredCode   = "ended_generation"
	duplicateReportIgnoredCode   = "duplicate_report"
	reportTooLargeErrorCode      = "report_too_large"
	fieldLimitErrorCode          = "field_limit_reached"
	duplicateFieldErrorCode      = "duplicate_field"
	missingIdentityErrorCode     = "missing_station_identity"
	wrongPasskeyErrorCode        = "wrong_passkey"
	unexpectedStationTypeErrCode = "unexpected_station_type"
	malformedReportErrorCode     = "malformed_report"
)

// Overflow and supersession classifications.
const (
	relayOverflowErrorCode      = "relay_overflow"
	pendingWorkErrorCode        = "pending_report_limit_reached"
	supersededWorkErrorCode     = "superseded_generation"
	supersededHealthErrorCode   = "superseded_health_transition"
	invalidMeasurementErrorCode = "invalid_measurement"
	sessionOperationErrorCode   = "session_operation_failed"
	upstreamErrorCode           = "upstream_connection_failed"
)

// reportParseErrorCode maps one parse failure to its fixed diagnostic code.
func reportParseErrorCode(err error) string {
	switch {
	case errors.Is(err, errReportTooLarge):
		return reportTooLargeErrorCode
	case errors.Is(err, errReportFieldLimit):
		return fieldLimitErrorCode
	case errors.Is(err, errReportDuplicateField):
		return duplicateFieldErrorCode
	case errors.Is(err, errReportMissingIdentity):
		return missingIdentityErrorCode
	case errors.Is(err, errReportWrongPasskey):
		return wrongPasskeyErrorCode
	case errors.Is(err, errReportUnexpectedStationType):
		return unexpectedStationTypeErrCode
	default:
		return malformedReportErrorCode
	}
}

// logIgnoredReport records one dropped report with an ignored-report
// classification and the payload byte count only.
func (ecowitt *Adapter) logIgnoredReport(ctx context.Context, code string, payloadBytes int) {
	ecowitt.logger.WarnContext(
		ctx,
		"ignored Ecowitt report",
		slog.String(eventKey, "adapter.report_ignored"),
		slog.String(errorCodeKey, code),
		slog.Int("payload_bytes", payloadBytes),
	)
}

// logRejectedReport records one parse rejection. It classifies the failure and
// reports the payload length; it never repeats the payload, the PASSKEY, the
// configured topic, or any field value.
func (ecowitt *Adapter) logRejectedReport(ctx context.Context, err error, payloadBytes int) {
	ecowitt.logger.WarnContext(
		ctx,
		"rejected Ecowitt report",
		slog.String(eventKey, "adapter.report_rejected"),
		slog.String(errorCodeKey, reportParseErrorCode(err)),
		slog.Int("payload_bytes", payloadBytes),
	)
}
