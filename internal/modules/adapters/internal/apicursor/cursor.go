package apicursor

import (
	"errors"
	"regexp"

	"github.com/mholtzscher/hearth/internal/platform/pagination"
)

const cursorVersion = 1

var adapterIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type idCursor struct {
	Version  int    `json:"v"`
	Resource string `json:"resource"`
	ID       string `json:"id"`
	DeviceID string `json:"device_id,omitempty"`
}

type healthCursor struct {
	Version      int    `json:"v"`
	Resource     string `json:"resource"`
	ParentID     string `json:"parent_id"`
	ReceiveOrder int64  `json:"receive_order"`
}

func EncodeAdapters(id string) (string, error) {
	return pagination.EncodeCursor(idCursor{Version: cursorVersion, Resource: "adapters", ID: id})
}

func DecodeAdapters(value string) (*string, error) {
	cursor, err := pagination.DecodeCursor[idCursor](value)
	if err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "adapters" || cursor.DeviceID != "" ||
		!adapterIDPattern.MatchString(cursor.ID) {
		return nil, errors.New("invalid Adapter cursor scope")
	}
	return &cursor.ID, nil
}

func EncodeHealth(adapterID string, receiveOrder int64) (string, error) {
	return pagination.EncodeCursor(
		healthCursor{
			Version:      cursorVersion,
			Resource:     "adapter_health",
			ParentID:     adapterID,
			ReceiveOrder: receiveOrder,
		},
	)
}

func DecodeHealth(value, adapterID string) (*int64, error) {
	cursor, err := pagination.DecodeCursor[healthCursor](value)
	if err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "adapter_health" || cursor.ParentID != adapterID ||
		cursor.ReceiveOrder < 1 {
		return nil, errors.New("invalid health cursor scope")
	}
	return &cursor.ReceiveOrder, nil
}
