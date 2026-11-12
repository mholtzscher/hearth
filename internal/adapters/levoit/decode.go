package levoit

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

func decodeEnvelope(data []byte) (json.RawMessage, error) {
	return decodeResponse(data, true)
}

// Successful mutations report an inner code without an inner result on real Core firmware.
func decodeAcknowledgement(data []byte) (json.RawMessage, error) {
	return decodeResponse(data, false)
}

func decodeResponse(data []byte, resultRequired bool) (json.RawMessage, error) {
	var envelope struct {
		Code           *int            `json:"code"`
		Result         json.RawMessage `json:"result"`
		Error          *int            `json:"error"`
		ErrorCode      *int            `json:"error_code"`
		CamelErrorCode *int            `json:"errorCode"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Code == nil {
		return nil, &providerError{code: reasonInvalidResponse}
	}
	for _, code := range []*int{envelope.Code, envelope.Error, envelope.ErrorCode, envelope.CamelErrorCode} {
		if code != nil && *code != 0 {
			return nil, codeError(*code)
		}
	}
	if resultRequired && !isObject(envelope.Result) {
		return nil, &providerError{code: reasonInvalidResponse}
	}
	return envelope.Result, nil
}

//nolint:mnd // External protocol error codes are literal vocabulary.
func codeError(code int) error {
	switch code {
	case -11260022, -11257129:
		return &providerError{code: reasonUnsupportedAuth}
	case -11001000:
		return &providerError{code: reasonSessionExpired}
	case -16906000, -11003000:
		return &providerError{code: reasonProviderThrottled}
	case 11, 4041004, -11300027, -11300000, -11302000, -11301000, -11005000, -11307000:
		return &providerError{code: "device_offline"}
	}
	switch code / 1000 * 1000 {
	case -11201000, -11202000, -11200000, -11203000:
		return &providerError{code: "authentication_rejected"}
	}
	return &providerError{code: reasonProviderUnavailable, uncertain: true}
}

func isObject(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) >= 2 && data[0] == '{' && data[len(data)-1] == '}'
}

func decodeStatus(data []byte, received time.Time) (status, error) {
	var fields map[string]json.RawMessage
	if !isObject(data) || json.Unmarshal(data, &fields) != nil {
		return status{}, &providerError{code: reasonInvalidResponse}
	}
	report := status{
		ReceivedAt: received,
		Power:      field[bool](fields["enabled"]),
		ChildLock:  field[bool](fields["child_lock"]),
		Mode:       field[string](fields["mode"]),
		PM25: boundedNumber(
			fields["air_quality_value"],
			maximumPM25,
		),
		FilterLife: boundedNumber(fields["filter_life"], maximumPercent),
	}
	if report.Mode != nil && *report.Mode != modeManual && *report.Mode != modeAuto && *report.Mode != modeSleep {
		report.Mode = nil
	}
	var configuration map[string]json.RawMessage
	if json.Unmarshal(fields["configuration"], &configuration) == nil {
		report.Display = field[bool](configuration["display"])
	}
	if raw, present := fields["levelNew"]; present {
		report.ManualLevel = boundedLevel(raw)
	} else if report.Mode != nil && *report.Mode == modeManual {
		report.ManualLevel = boundedLevel(fields["level"])
	}
	return report, nil
}

func field[T any](data []byte) *T {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	var value T
	if json.Unmarshal(data, &value) != nil {
		return nil
	}
	return &value
}

func boundedNumber(data []byte, maximum float64) *float64 {
	value := field[float64](data)
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > maximum {
		return nil
	}
	return value
}

func boundedLevel(data []byte) *int {
	value := field[int](data)
	if value == nil || *value < 1 || *value > 4 {
		return nil
	}
	return value
}

func (c *client) devices(ctx context.Context) ([]device, error) {
	const pageSize, maximumPages = 100, 100
	var devices []device
	seen := make(map[string]bool)
	count := 0
	for page := 1; page <= maximumPages; page++ {
		result, err := c.authorized(ctx, inventoryPath, func(session authSession) map[string]any {
			body := c.common(session)
			body["method"], body["pageNo"], body["pageSize"] = "devices", page, pageSize
			return body
		}, true, nil)
		if err != nil {
			return nil, err
		}
		var inventory struct {
			Page  *int              `json:"pageNo"`
			Total *int              `json:"total"`
			List  []json.RawMessage `json:"list"`
		}
		if json.Unmarshal(result, &inventory) != nil || inventory.List == nil ||
			(inventory.Page != nil && *inventory.Page != page) || (inventory.Total != nil && *inventory.Total < 0) {
			return nil, &providerError{code: reasonInvalidResponse}
		}
		entries, decodeErr := decodeInventoryEntries(inventory.List, seen)
		if decodeErr != nil {
			return nil, decodeErr
		}
		devices = append(devices, entries...)
		count += len(inventory.List)
		if inventory.Total != nil {
			if count >= *inventory.Total {
				return devices, nil
			}
			if len(inventory.List) == 0 {
				return nil, &providerError{code: reasonInvalidResponse}
			}
		} else if len(inventory.List) < pageSize {
			return devices, nil
		}
	}
	return nil, &providerError{code: reasonInvalidResponse}
}

func decodeInventoryEntries(list []json.RawMessage, seen map[string]bool) ([]device, error) {
	var devices []device
	for _, raw := range list {
		var identity struct {
			CID *string `json:"cid"`
		}
		if json.Unmarshal(raw, &identity) != nil {
			return nil, &providerError{code: reasonInvalidResponse}
		}
		if identity.CID != nil && *identity.CID != "" {
			if seen[*identity.CID] {
				return nil, &providerError{code: reasonInvalidResponse}
			}
			seen[*identity.CID] = true
		}
		d, supported, err := decodeDevice(raw)
		if err != nil {
			return nil, err
		}
		if !supported {
			continue
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func decodeDevice(raw []byte) (device, bool, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return device{}, false, &providerError{code: reasonInvalidResponse}
	}
	model := field[string](fields["deviceType"])
	if model == nil || *model == "" {
		return device{}, false, &providerError{code: reasonInvalidResponse}
	}
	name := supportedModel(*model)
	if name == "" {
		return device{}, false, nil
	}
	cid, module, region := field[string](
		fields["cid"],
	), field[string](
		fields["configModule"],
	), field[string](
		fields["deviceRegion"],
	)
	if cid == nil || strings.TrimSpace(*cid) == "" || utf8.RuneCountInString(*cid) > 220 ||
		module == nil || *module == "" || len(*module) > 256 || (region != nil && *region != "US") {
		return device{}, false, &providerError{code: reasonInvalidResponse}
	}
	if displayName := field[string](fields["deviceName"]); displayName != nil {
		if trimmed := strings.TrimSpace(*displayName); trimmed != "" {
			runes := []rune(trimmed)
			if len(runes) > maximumNameRunes {
				runes = runes[:maximumNameRunes]
			}
			name = string(runes)
		}
	}
	connection := field[string](fields["connectionStatus"])
	var properties map[string]json.RawMessage
	if json.Unmarshal(fields["deviceProp"], &properties) == nil {
		if value := field[string](properties["connectionStatus"]); value != nil {
			connection = value
		}
	}
	// Unknown reachability still permits a bounded status probe; explicit offline does not.
	online := connection == nil || *connection == "online"
	return device{CID: *cid, Name: name, Model: *model, ConfigModule: *module, Region: "US", Online: online}, true, nil
}

func supportedModel(model string) string {
	switch model {
	case "Core400S", "LAP-C401S-WJP", "LAP-C401S-WUSR", "LAP-C401S-WAAA":
		return "Core 400S"
	case "Core600S", "LAP-C601S-WUS", "LAP-C601S-WUSR", "LAP-C601S-WEU":
		return "Core 600S"
	default:
		return ""
	}
}
