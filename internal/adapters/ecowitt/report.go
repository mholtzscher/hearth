package ecowitt

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"
)

const (
	// maximumReportBytes bounds one Customized Server form body before it is
	// parsed, so a hostile or truncated broker payload cannot drive unbounded
	// work.
	maximumReportBytes = 64 << 10
	// maximumReportFields bounds the decoded key count.
	maximumReportFields = 256
	// passkeyHexDigits is the exact length of a hexadecimal PASSKEY.
	passkeyHexDigits = 32
	// stationTypePrefix is the only accepted station family. The captured
	// GW2000B_V3.3.2 identity satisfies this prefix rule.
	stationTypePrefix = "GW2000"
	// passkeyField, stationTypeField, and dateUTCField are the required
	// identity fields.
	passkeyField     = "PASSKEY"
	stationTypeField = "stationtype"
	dateUTCField     = "dateutc"
	// modelField is the optional gateway model.
	modelField = "model"
	// sourceTimeLayout is Ecowitt's Customized Server dateutc layout in UTC.
	sourceTimeLayout = "2006-01-02 15:04:05"
	// maximumSourceTimeAhead bounds how far past the receipt time a station
	// clock may be before its source time is discarded as implausible.
	maximumSourceTimeAhead = 5 * time.Minute
)

// Report parse failures. They are plain sentinels so a parse error can never
// carry payload content, a PASSKEY, or a field value into a diagnostic.
var (
	errReportTooLarge              = errors.New("ecowitt report exceeds the maximum payload size")
	errReportMalformed             = errors.New("ecowitt report is not a valid URL-encoded form")
	errReportFieldLimit            = errors.New("ecowitt report exceeds the maximum field count")
	errReportDuplicateField        = errors.New("ecowitt report repeats a field name")
	errReportMissingIdentity       = errors.New("ecowitt report omits a required station identity field")
	errReportWrongPasskey          = errors.New("ecowitt report does not match the configured PASSKEY")
	errReportUnexpectedStationType = errors.New("ecowitt report is not a GW2000 station")
)

// stationReport is one structurally valid, compatible Customized Server
// report. Fields is the decoded form without the PASSKEY key, so a report that
// is later formatted or logged can never repeat the configured secret.
type stationReport struct {
	Passkey     [16]byte
	StationType string
	Model       string
	DateUTC     string
	SourceTime  *time.Time
	Fields      map[string]string
}

// reportSignature identifies one accepted report so immediate MQTT
// redelivery is not mistaken for new station evidence. It is connection-local
// memory and does not survive restart.
type reportSignature struct {
	DateUTC    string
	PayloadSHA [32]byte
}

// newReportSignature derives the duplicate signature from the decoded station
// date and the SHA-256 of the original payload bytes. The payload hash covers
// every field, so two reports with the same dateutc but different measurements
// stay distinct.
func newReportSignature(report stationReport, payload []byte) reportSignature {
	return reportSignature{DateUTC: report.DateUTC, PayloadSHA: sha256.Sum256(payload)}
}

// parseStationReport decodes one Customized Server form body and validates the
// station identity. An error means the whole report is rejected and produces
// no evidence. Measurement fields are not validated here: an absent or
// malformed measurement is isolated per Entity by the capability catalog.
func parseStationReport(
	payload []byte,
	receivedAt time.Time,
	expectedPasskey [16]byte,
) (stationReport, error) {
	if len(payload) > maximumReportBytes {
		return stationReport{}, errReportTooLarge
	}
	// ParseQuery is the standard form decoder: it maps '+' to space, percent
	// decodes, and rejects malformed escapes. Its error is deliberately
	// discarded so no fragment of the payload can reach a diagnostic.
	values, err := url.ParseQuery(string(payload))
	if err != nil {
		return stationReport{}, errReportMalformed
	}
	if len(values) > maximumReportFields {
		return stationReport{}, errReportFieldLimit
	}
	fields := make(map[string]string, len(values))
	for key, list := range values {
		if key == "" {
			return stationReport{}, errReportMalformed
		}
		if len(list) != 1 {
			return stationReport{}, errReportDuplicateField
		}
		fields[key] = list[0]
	}
	passkey, err := decodePasskey(fields[passkeyField])
	if err != nil {
		return stationReport{}, err
	}
	if subtle.ConstantTimeCompare(passkey[:], expectedPasskey[:]) != 1 {
		return stationReport{}, errReportWrongPasskey
	}
	stationType := fields[stationTypeField]
	if stationType == "" {
		return stationReport{}, errReportMissingIdentity
	}
	if !strings.HasPrefix(stationType, stationTypePrefix) {
		return stationReport{}, errReportUnexpectedStationType
	}
	dateUTC := fields[dateUTCField]
	if dateUTC == "" {
		return stationReport{}, errReportMissingIdentity
	}
	delete(fields, passkeyField)
	return stationReport{
		Passkey:     passkey,
		StationType: stationType,
		Model:       fields[modelField],
		DateUTC:     dateUTC,
		SourceTime:  parseSourceTime(dateUTC, receivedAt),
		Fields:      fields,
	}, nil
}

// decodePasskey validates the exact hexadecimal PASSKEY shape and decodes it
// for constant-time comparison. A missing or malformed value is a missing
// station identity; only a well-formed value can be compared as a secret.
func decodePasskey(value string) ([16]byte, error) {
	var passkey [16]byte
	if value == "" {
		return passkey, errReportMissingIdentity
	}
	if len(value) != passkeyHexDigits {
		return passkey, errReportMalformed
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return passkey, errReportMalformed
	}
	copy(passkey[:], decoded)
	return passkey, nil
}

// parseSourceTime accepts dateutc as the report's optional source time only
// when it parses, is not older than 2000-01-01 UTC, and is not more than five
// minutes ahead of the local receipt time. An implausible station clock leaves
// the source time absent; the report itself is still accepted, because the
// local receipt time is authoritative evidence.
func parseSourceTime(value string, receivedAt time.Time) *time.Time {
	parsed, err := time.ParseInLocation(sourceTimeLayout, value, time.UTC)
	if err != nil {
		return nil
	}
	if parsed.Before(sourceTimeFloor()) {
		return nil
	}
	if parsed.After(receivedAt.UTC().Add(maximumSourceTimeAhead)) {
		return nil
	}
	return &parsed
}

// sourceTimeFloor is the earliest station clock the Adapter trusts. A gateway
// that has never synchronised its clock reports a 1970-era dateutc, which must
// not become a source timestamp.
func sourceTimeFloor() time.Time {
	return time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
}
