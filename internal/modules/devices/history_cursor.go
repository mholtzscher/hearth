package devices

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"
)

type historyCursor struct {
	Version      int       `json:"v"`
	Resource     string    `json:"resource"`
	Fingerprint  string    `json:"query"`
	ObservedAt   time.Time `json:"observed_at"`
	ReceiveOrder int64     `json:"receive_order"`
	HighWater    int64     `json:"high_water"`
}

func historyFingerprint(query HistoryQuery, result HistoryObservations) string {
	selectExpr := "sample.value"
	if query.SelectExpr != nil {
		selectExpr = *query.SelectExpr
	}
	payload, _ := json.Marshal(struct {
		Entity EntityID     `json:"entity"`
		From   string       `json:"from"`
		To     string       `json:"to"`
		Select string       `json:"select"`
		Where  *string      `json:"where"`
		Order  HistoryOrder `json:"order"`
		Limit  int          `json:"limit"`
	}{query.EntityID, query.Range.From.Format(time.RFC3339Nano), query.Range.To.Format(time.RFC3339Nano), selectExpr, result.WhereExpr, result.Order, result.Limit})
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func encodeHistoryCursor(
	query HistoryQuery,
	result HistoryObservations,
	row EntityStateHistoryEntry,
	highWater int64,
) string {
	cursor := historyCursor{
		Version:      1,
		Resource:     "entity-state-history-query",
		Fingerprint:  historyFingerprint(query, result),
		ObservedAt:   row.ObservedAt.UTC(),
		ReceiveOrder: row.ReceiveOrder,
		HighWater:    highWater,
	}
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

//nolint:nilnil // A nil cursor is the explicit first-page position.
func decodeHistoryCursor(query HistoryQuery, result HistoryObservations) (*HistoryReadCursor, error) {
	if result.Cursor == "" {
		return nil, nil
	}
	fail := func() (*HistoryReadCursor, error) { return nil, historyError("invalid_cursor", "result.cursor") }
	payload, err := base64.RawURLEncoding.DecodeString(result.Cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(payload) != result.Cursor {
		return fail()
	}
	var cursor historyCursor
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF {
		return fail()
	}
	canonical, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(payload, canonical) {
		return fail()
	}
	if cursor.Version != 1 || cursor.Resource != "entity-state-history-query" ||
		cursor.Fingerprint != historyFingerprint(query, result) ||
		cursor.ObservedAt.Location() != time.UTC ||
		cursor.ObservedAt.Before(query.Range.From) ||
		!cursor.ObservedAt.Before(query.Range.To) ||
		cursor.ReceiveOrder < 1 ||
		cursor.HighWater < cursor.ReceiveOrder {
		return fail()
	}
	return &HistoryReadCursor{
		ObservedAt:   cursor.ObservedAt,
		ReceiveOrder: cursor.ReceiveOrder,
		HighWater:    cursor.HighWater,
	}, nil
}
