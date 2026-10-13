package simulator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
)

type notificationRequestsResponse struct {
	Items    []scripted.NotificationCapture         `json:"items"`
	Metadata []scripted.NotificationCaptureMetadata `json:"metadata"`
	HasMore  bool                                   `json:"has_more"`
}

//nolint:gocognit // Routes share destination lookup and translate runtime errors at this boundary.
func registerNotificationControls(mux *http.ServeMux, runtimes map[string]*scripted.NotificationRuntime) {
	lookup := func(id string) *scripted.NotificationRuntime {
		for _, runtime := range runtimes {
			for _, destination := range runtime.Destinations() {
				if destination.DestinationID == id {
					return runtime
				}
			}
		}
		return nil
	}
	mux.HandleFunc("GET /v1/sim/notifications/destinations", func(w http.ResponseWriter, _ *http.Request) {
		items := make([]scripted.NotificationDestinationInfo, 0)
		for _, runtime := range runtimes {
			items = append(items, runtime.Destinations()...)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].AdapterID != items[j].AdapterID {
				return items[i].AdapterID < items[j].AdapterID
			}
			return items[i].Key < items[j].Key
		})
		writeControlJSON(w, http.StatusOK, items)
	})
	mux.HandleFunc(
		"PUT /v1/sim/notifications/destinations/{id}/behavior",
		func(w http.ResponseWriter, r *http.Request) {
			behavior, err := decodeNotificationBehavior(w, r)
			if err != nil {
				writeControlError(w, http.StatusBadRequest, err.Error())
				return
			}
			runtime := lookup(r.PathValue("id"))
			if runtime == nil {
				writeControlError(w, http.StatusNotFound, "unknown notification destination")
				return
			}
			if err = runtime.SetBehavior(r.PathValue("id"), behavior); err != nil {
				writeControlError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeControlJSON(w, http.StatusOK, behavior)
		},
	)
	mux.HandleFunc("GET /v1/sim/notifications/requests", func(w http.ResponseWriter, r *http.Request) {
		page, err := notificationRequests(r, runtimes)
		if err != nil {
			writeControlError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeControlJSON(w, http.StatusOK, page)
	})
	mux.HandleFunc(
		"POST /v1/sim/notifications/requests/{attempt_id}/release",
		func(w http.ResponseWriter, r *http.Request) {
			for _, runtime := range runtimes {
				if err := runtime.Release(r.PathValue("attempt_id")); err == nil {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			writeControlError(w, http.StatusNotFound, "unknown notification attempt")
		},
	)
}

// Decode a complete replacement: omission, null, unknown fields and extra JSON
// values are errors. reason_code is optional but cannot be explicitly null.
func decodeNotificationBehavior(w http.ResponseWriter, r *http.Request) (scripted.NotificationBehavior, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, controlMaxBody))
	if err := decoder.Decode(&fields); err != nil {
		return scripted.NotificationBehavior{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return scripted.NotificationBehavior{}, errors.New("extra JSON content")
	}
	for name, raw := range fields {
		if name != "outcome" && name != "reason_code" && name != "hold" {
			return scripted.NotificationBehavior{}, fmt.Errorf("unknown field %q", name)
		}
		if string(raw) == "null" {
			return scripted.NotificationBehavior{}, fmt.Errorf("%s cannot be null", name)
		}
	}
	if fields["outcome"] == nil || fields["hold"] == nil {
		return scripted.NotificationBehavior{}, errors.New("outcome and hold are required")
	}
	var behavior scripted.NotificationBehavior
	if err := json.Unmarshal(fields["outcome"], &behavior.Outcome); err != nil {
		return behavior, err
	}
	if err := json.Unmarshal(fields["hold"], &behavior.Hold); err != nil {
		return behavior, err
	}
	if raw := fields["reason_code"]; raw != nil {
		if err := json.Unmarshal(raw, &behavior.ReasonCode); err != nil {
			return behavior, err
		}
	}
	return behavior, behavior.Validate()
}

func notificationRequests(
	r *http.Request,
	runtimes map[string]*scripted.NotificationRuntime,
) (notificationRequestsResponse, error) {
	adapterID, after, limit, err := notificationRequestQuery(r)
	if err != nil {
		return notificationRequestsResponse{}, err
	}
	return notificationRequestPage(adapterID, after, limit, runtimes)
}

func notificationRequestQuery(r *http.Request) (string, uint64, int, error) {
	query := r.URL.Query()
	adapterID := query.Get("adapter_id")
	var after uint64
	if values, supplied := query["after_sequence"]; supplied {
		if adapterID == "" || len(values) != 1 {
			return "", 0, 0, errors.New("after_sequence requires adapter_id and one cursor")
		}
		var err error
		after, err = strconv.ParseUint(values[0], 10, 64)
		if err != nil {
			return "", 0, 0, errors.New("invalid after_sequence")
		}
	}
	limit := 100
	if values, supplied := query["limit"]; supplied {
		if len(values) != 1 {
			return "", 0, 0, errors.New("one limit required")
		}
		var err error
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > scripted.NotificationCaptureLimit {
			return "", 0, 0, errors.New("limit must be 1-1000")
		}
	}
	return adapterID, after, limit, nil
}

func notificationRequestPage(
	adapterID string,
	after uint64,
	limit int,
	runtimes map[string]*scripted.NotificationRuntime,
) (notificationRequestsResponse, error) {
	if adapterID != "" && runtimes[adapterID] == nil {
		return notificationRequestsResponse{}, errors.New("unknown adapter_id")
	}
	result := notificationRequestsResponse{
		Items:    make([]scripted.NotificationCapture, 0),
		Metadata: make([]scripted.NotificationCaptureMetadata, 0),
	}
	for id, runtime := range runtimes {
		if adapterID != "" && id != adapterID {
			continue
		}
		page, err := runtime.Requests(after, limit)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, page.Items...)
		result.Metadata = append(result.Metadata, page.Metadata)
		result.HasMore = result.HasMore || page.HasMore
	}
	sort.Slice(
		result.Metadata,
		func(i, j int) bool { return result.Metadata[i].AdapterID < result.Metadata[j].AdapterID },
	)
	sort.Slice(result.Items, func(i, j int) bool {
		left, right := result.Items[i], result.Items[j]
		if !left.ReceivedAt.Equal(right.ReceivedAt) {
			return left.ReceivedAt.Before(right.ReceivedAt)
		}
		if left.AdapterID != right.AdapterID {
			return left.AdapterID < right.AdapterID
		}
		return left.Sequence < right.Sequence
	})
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.HasMore = true
	}
	return result, nil
}
