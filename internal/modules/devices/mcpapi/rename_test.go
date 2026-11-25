package mcpapi //nolint:testpackage // Exercises the registered rename tools over the module's wire fixture.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

const renameAdapterName = "Hue lamp"

// renameStore persists name edits after the real devices.Service has
// normalized them, so these tests cover the domain validation the tools rely on.
type renameStore struct {
	device devices.PatchDeviceParams
	entity devices.PatchEntityParams
	err    error
	calls  int
}

func (store *renameStore) PatchDevice(_ context.Context, params devices.PatchDeviceParams) (devices.Device, error) {
	store.calls++
	store.device = params
	if store.err != nil {
		return devices.Device{}, store.err
	}
	override := params.Patch.NameEdit.Override
	return devices.Device{
		ID: params.DeviceID, Kind: devices.DeviceKindLight, Name: effectiveName(override),
		AdapterName: renameAdapterName, NameOverride: override,
	}, nil
}

func (store *renameStore) PatchEntity(
	_ context.Context,
	params devices.PatchEntityParams,
) (devices.EntityWithState, error) {
	store.calls++
	store.entity = params
	if store.err != nil {
		return devices.EntityWithState{}, store.err
	}
	view := apiEntityWithState(nil)
	override := params.Patch.NameEdit.Override
	view.Entity.Name, view.Entity.AdapterName, view.Entity.NameOverride = effectiveName(
		override,
	), renameAdapterName, override
	return view, nil
}

func effectiveName(override *string) string {
	if override != nil {
		return *override
	}
	return renameAdapterName
}

func renameServer(store *renameStore) http.Handler {
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
	service := devices.NewService(devices.Stores{Metadata: store}, nil, nil, devices.Dependencies{})
	Register(server, service, testPresenter())
	return server.HTTPHandler()
}

func TestRenameToolsSetAndClearTheNameOverride(t *testing.T) {
	t.Parallel()
	replace := strings.NewReplacer("$D", string(apiDeviceID), "$E", string(apiEntityID))
	for _, test := range []struct {
		name, tool, arguments, want string
		override                    *string
	}{
		{
			"device set", "rename_device", `{"device_id":"$D","name_override":"  Reading lamp "}`,
			`{"device_id":"$D","name":"Reading lamp","adapter_name":"Hue lamp","name_override":"Reading lamp"}`,
			new("Reading lamp"),
		},
		{
			"device clear", "rename_device", `{"device_id":"$D","name_override":null}`,
			`{"device_id":"$D","name":"Hue lamp","adapter_name":"Hue lamp","name_override":null}`,
			nil,
		},
		{
			"entity set", "rename_entity", `{"entity_id":"$E","name_override":"Reading lamp"}`,
			`{"entity_id":"$E","name":"Reading lamp","adapter_name":"Hue lamp","name_override":"Reading lamp"}`,
			new("Reading lamp"),
		},
		{
			"entity clear", "rename_entity", `{"entity_id":"$E","name_override":null}`,
			`{"entity_id":"$E","name":"Hue lamp","adapter_name":"Hue lamp","name_override":null}`,
			nil,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &renameStore{}
			result := callWire(t, renameServer(store), test.tool, replace.Replace(test.arguments))
			assertWireJSON(t, result, replace.Replace(test.want))

			edit := store.device.Patch.NameEdit
			if test.tool == "rename_entity" {
				edit = store.entity.Patch.NameEdit
				if store.entity.EntityID != apiEntityID || store.entity.Patch.Enabled != nil {
					t.Fatalf("entity patch = %+v", store.entity)
				}
			} else if store.device.DeviceID != apiDeviceID {
				t.Fatalf("device patch = %+v", store.device)
			}
			if store.calls != 1 || edit == nil || !equalOverride(edit.Override, test.override) {
				t.Fatalf("calls = %d, name edit = %+v, want override %v", store.calls, edit, test.override)
			}
		})
	}
}

func TestRenameToolsRejectInvalidNamesBeforePersistence(t *testing.T) {
	t.Parallel()
	const nameMessage = "name_override must be 1 to 128 code points after trimming and contain no control characters"
	for _, test := range []struct {
		name, tool, arguments, code, message string
	}{
		{"device missing override", "rename_device", `{"device_id":"$D"}`, "invalid_arguments", ""},
		{"entity missing override", "rename_entity", `{"entity_id":"$E"}`, "invalid_arguments", ""},
		{"device non-string override", "rename_device", `{"device_id":"$D","name_override":5}`, "invalid_arguments", ""},
		{"device blank", "rename_device", `{"device_id":"$D","name_override":"   "}`, "invalid_request", nameMessage},
		{"entity blank", "rename_entity", `{"entity_id":"$E","name_override":""}`, "invalid_request", nameMessage},
		{
			"device too long", "rename_device",
			`{"device_id":"$D","name_override":"` + strings.Repeat("é", 129) + `"}`,
			"invalid_request", nameMessage,
		},
		{
			"entity control character", "rename_entity", `{"entity_id":"$E","name_override":"Lamp\u0007"}`,
			"invalid_request", nameMessage,
		},
		{"device malformed ID", "rename_device", `{"device_id":"dev_bad","name_override":"Lamp"}`, "invalid_device_id", ""},
		{"entity malformed ID", "rename_entity", `{"entity_id":"ent_bad","name_override":"Lamp"}`, "invalid_entity_id", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &renameStore{}
			arguments := strings.NewReplacer("$D", string(apiDeviceID), "$E", string(apiEntityID)).
				Replace(test.arguments)
			failure := renameFailureResult(t, callWire(t, renameServer(store), test.tool, arguments))
			if failure.Code != test.code || (test.message != "" && failure.Message != test.message) {
				t.Fatalf("failure = %+v, want %q / %q", failure, test.code, test.message)
			}
			if store.calls != 0 {
				t.Fatalf("invalid input reached persistence %d times", store.calls)
			}
		})
	}
}

func TestRenameToolsMapPersistenceFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, tool, arguments string
		err                   error
		code                  string
	}{
		{
			"missing device", "rename_device", `{"device_id":"` + string(apiDeviceID) + `","name_override":"Lamp"}`,
			devices.ErrDeviceNotFound, "device_not_found",
		},
		{
			"missing entity", "rename_entity", `{"entity_id":"` + string(apiEntityID) + `","name_override":null}`,
			devices.ErrEntityNotFound, "entity_not_found",
		},
		{
			"storage fault", "rename_device", `{"device_id":"` + string(apiDeviceID) + `","name_override":"Lamp"}`,
			errors.New("PRIVATE storage fault"), mcptools.CodeInternalError,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &renameStore{err: test.err}
			result := callWire(t, renameServer(store), test.tool, test.arguments)
			if failure := renameFailureResult(t, result); failure.Code != test.code {
				t.Fatalf("failure = %+v, want %q", failure, test.code)
			}
			if strings.Contains(string(result.Structured), "PRIVATE") {
				t.Fatalf("failure published its cause: %s", result.Structured)
			}
		})
	}
}

type renameFailureBody struct {
	Code    string `json:"failure_code"`
	Message string `json:"message"`
}

func renameFailureResult(t *testing.T, result wireResult) renameFailureBody {
	t.Helper()
	var failure renameFailureBody
	if !result.IsError || len(result.Content) != 1 || string(result.Structured) != result.Content[0].Text ||
		json.Unmarshal(result.Structured, &failure) != nil {
		t.Fatalf("result = %+v, want a structured tool failure", result)
	}
	return failure
}

func equalOverride(got, want *string) bool {
	if got == nil || want == nil {
		return got == want
	}
	return *got == *want
}
