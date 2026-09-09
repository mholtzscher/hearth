package zigbee2mqtt //nolint:testpackage // Tests exercise package-private constructor wiring and safe catalog diagnostics.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// This test protects the explicit profile catalog dependency and fails if the
// constructor accepts a nil or zero catalog or stores anything other than the
// caller-supplied catalog. The defect would be an adapter that starts without
// compiled profiles and dereferences nil or plans from the wrong catalog at
// discovery time.
func TestNewRequiresLoaderProducedProfileCatalog(t *testing.T) {
	t.Parallel()
	session := &fakeSession{}
	config := Config{MQTTURL: "tcp://127.0.0.1:1883", BaseTopic: "zigbee2mqtt", ClientID: "test-client"}
	logger := slog.New(slog.DiscardHandler)
	dialer := &fakeDialer{}

	for _, catalog := range []*ProfileCatalog{nil, {}} {
		if _, err := newAdapter(session, config, catalog, logger, dialer); err == nil {
			t.Fatalf("newAdapter with catalog %#v succeeded, want profile catalog rejection", catalog)
		} else if !strings.Contains(err.Error(), "Zigbee2MQTT profile catalog is required") {
			t.Fatalf("newAdapter error = %q, want the profile catalog rejection", err)
		}
	}

	catalog := mustEmbeddedProfileCatalog(t)
	constructed, err := newAdapter(session, config, catalog, logger, dialer)
	if err != nil {
		t.Fatal(err)
	}
	if constructed.profiles != catalog {
		t.Fatal("adapter stored a different profile catalog than the constructor received")
	}
}

// This test protects the safe catalog-failure diagnostic and fails if the log
// record omits the catalog error code, profile ID, rule ID, or JSON pointer,
// or if it repeats raw profile contents carried by the wrapped error. The
// defect would be an operator-unactionable startup log or a secret-bearing
// profile value echoed into process logs.
func TestLogProfileCatalogFailureEmitsSafeEvidence(t *testing.T) {
	t.Parallel()
	handler := newCaptureHandler()
	logger := slog.New(handler)
	rawValue := "SECRET-RAW-PROFILE-VALUE"
	catalogErr := &ProfileCatalogError{
		Code:        profileCatalogErrorSchemaInvalid,
		Document:    "profiles/light.profile.json",
		ProfileID:   "light",
		RuleID:      "light.power",
		JSONPointer: "/candidate_groups/0/entities/0",
		Err:         errors.New("strategy parameters carry " + rawValue),
	}

	LogProfileCatalogFailure(context.Background(), logger, catalogErr)

	record, found := handler.first(slog.LevelError, "adapter.profile_catalog_failed")
	if !found {
		t.Fatal("missing adapter.profile_catalog_failed diagnostic")
	}
	for field, want := range map[string]string{
		"component":    adapterComponent,
		"error_code":   profileCatalogErrorSchemaInvalid,
		"profile_id":   "light",
		"rule_id":      "light.power",
		"json_pointer": "/candidate_groups/0/entities/0",
	} {
		value, ok := record.attrs[field].(string)
		if !ok || value != want {
			t.Fatalf("diagnostic field %q = %#v, want %q in %#v", field, record.attrs[field], want, record.attrs)
		}
	}
	for key, value := range record.attrs {
		if strings.Contains(fmt.Sprintf("%v", value), rawValue) {
			t.Fatalf("diagnostic field %q repeats raw profile contents: %#v", key, record.attrs)
		}
	}
}
