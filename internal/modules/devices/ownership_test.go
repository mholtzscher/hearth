package devices //nolint:testpackage // Tests exercise package-private domain seams.

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// testAdapterRuntime returns the runtime identity the named Adapter claims in
// owned-mapping fixtures.
func testAdapterRuntime(adapterID string) RuntimeID {
	if adapterID == "homeassistant" {
		return RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ad")
	}
	return testRuntimeID
}

type ownedMappingRepositoryStub struct {
	instance AdapterInstance
	getErr   error
	page     Page[OwnedMapping]
	listErr  error
	calls    []string
	params   ListOwnedMappingsParams
}

func (repository *ownedMappingRepositoryStub) GetAdapter(
	_ context.Context,
	_ string,
) (AdapterInstance, error) {
	repository.calls = append(repository.calls, "get")
	return repository.instance, repository.getErr
}

func (repository *ownedMappingRepositoryStub) ListOwnedMappings(
	_ context.Context,
	params ListOwnedMappingsParams,
) (Page[OwnedMapping], error) {
	repository.calls = append(repository.calls, "list")
	repository.params = params
	return repository.page, repository.listErr
}

func TestListOwnedMappingsRejectsInvalidInputsBeforeRepositoryAccess(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		adapterID string
		runtimeID RuntimeID
		page      OwnedMappingPageParams
	}{
		"invalid Adapter ID": {
			adapterID: "HomeAssistant", runtimeID: commandTestRuntimeID,
			page: OwnedMappingPageParams{Limit: 1},
		},
		"invalid runtime ID": {
			adapterID: "homeassistant", runtimeID: "not-a-runtime",
			page: OwnedMappingPageParams{Limit: 1},
		},
		"zero limit": {
			adapterID: "homeassistant", runtimeID: commandTestRuntimeID,
			page: OwnedMappingPageParams{Limit: 0},
		},
		"limit above maximum": {
			adapterID: "homeassistant", runtimeID: commandTestRuntimeID,
			page: OwnedMappingPageParams{Limit: 201},
		},
		"position without Binding key": {
			adapterID: "homeassistant", runtimeID: commandTestRuntimeID,
			page: OwnedMappingPageParams{After: &OwnedMappingPosition{EntityKey: "power"}, Limit: 1},
		},
		"position without Entity key": {
			adapterID: "homeassistant", runtimeID: commandTestRuntimeID,
			page: OwnedMappingPageParams{After: &OwnedMappingPosition{BindingKey: "office-light"}, Limit: 1},
		},
		"malformed Binding key": {
			adapterID: "homeassistant", runtimeID: commandTestRuntimeID,
			page: OwnedMappingPageParams{
				After: &OwnedMappingPosition{BindingKey: "Office Light", EntityKey: "power"}, Limit: 1,
			},
		},
		"malformed Entity key": {
			adapterID: "homeassistant", runtimeID: commandTestRuntimeID,
			page: OwnedMappingPageParams{
				After: &OwnedMappingPosition{BindingKey: "office-light", EntityKey: "Power"}, Limit: 1,
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := &ownedMappingRepositoryStub{}
			service := newTestService(repository, nil, nil, Dependencies{})

			_, err := service.ListOwnedMappings(
				context.Background(), test.adapterID, test.runtimeID, test.page,
			)
			if !errors.Is(err, ErrInvalidPage) {
				t.Fatalf("error = %v, want ErrInvalidPage", err)
			}
			if len(repository.calls) != 0 {
				t.Fatalf("repository calls = %v, want none", repository.calls)
			}
		})
	}
}

func TestListOwnedMappingsFencesInactiveRuntimeBeforeMappingRead(t *testing.T) {
	t.Parallel()
	tests := map[string]ownedMappingRepositoryStub{
		"unknown Adapter": {getErr: ErrAdapterNotFound},
		"released or expired runtime": {
			instance: AdapterInstance{ID: "homeassistant"},
		},
		"offline runtime": {
			instance: AdapterInstance{ID: "homeassistant", Health: AdapterHealth{Runtime: &RuntimeEvidence{
				ID: commandTestRuntimeID, Status: RuntimeStatusOffline,
			}}},
		},
		"superseded runtime": {
			instance: AdapterInstance{ID: "homeassistant", Health: AdapterHealth{Runtime: &RuntimeEvidence{
				ID: testAdapterRuntime("homeassistant"), Status: RuntimeStatusOnline,
			}}},
		},
	}
	for name, configured := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := configured
			service := newTestService(&repository, nil, nil, Dependencies{})

			_, err := service.ListOwnedMappings(
				context.Background(), "homeassistant", commandTestRuntimeID, OwnedMappingPageParams{Limit: 50},
			)
			if !errors.Is(err, ErrRuntimeFenced) {
				t.Fatalf("error = %v, want ErrRuntimeFenced", err)
			}
			if !reflect.DeepEqual(repository.calls, []string{"get"}) {
				t.Fatalf("repository calls = %v, want runtime check only", repository.calls)
			}
		})
	}
}

func TestListOwnedMappingsVerifiesRuntimeThenScopesAndCopiesRepositoryPage(t *testing.T) {
	t.Parallel()
	position := &OwnedMappingPosition{BindingKey: "office-light", EntityKey: "brightness"}
	repository := &ownedMappingRepositoryStub{
		instance: AdapterInstance{ID: "homeassistant", Health: AdapterHealth{Runtime: &RuntimeEvidence{
			ID: commandTestRuntimeID, Status: RuntimeStatusOnline,
		}}},
		page: Page[OwnedMapping]{Items: []OwnedMapping{{
			BindingKey: "office-light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			EntityKey: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		}}, HasMore: true},
	}
	service := newTestService(repository, nil, nil, Dependencies{})

	page, err := service.ListOwnedMappings(
		context.Background(), "homeassistant", commandTestRuntimeID,
		OwnedMappingPageParams{After: position, Limit: 200},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repository.calls, []string{"get", "list"}) {
		t.Fatalf("repository calls = %v", repository.calls)
	}
	wantParams := ListOwnedMappingsParams{AdapterID: "homeassistant", After: position, Limit: 200}
	if !reflect.DeepEqual(repository.params, wantParams) {
		t.Fatalf("repository params = %#v, want %#v", repository.params, wantParams)
	}
	if !page.HasMore || !reflect.DeepEqual(page.Items, repository.page.Items) {
		t.Fatalf("page = %#v, want %#v", page, repository.page)
	}
	repository.page.Items[0].BindingKey = "mutated"
	if page.Items[0].BindingKey != "office-light" {
		t.Fatalf("returned items alias repository slice: %#v", page.Items)
	}
}

func TestListOwnedMappingsReturnsRepositoryErrors(t *testing.T) {
	t.Parallel()
	getFailure := errors.New("get Adapter failed")
	listFailure := errors.New("list mappings failed")
	for name, repository := range map[string]*ownedMappingRepositoryStub{
		"runtime verification": {getErr: getFailure},
		"mapping read": {
			instance: AdapterInstance{Health: AdapterHealth{Runtime: &RuntimeEvidence{
				ID: commandTestRuntimeID, Status: RuntimeStatusOnline,
			}}},
			listErr: listFailure,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			service := newTestService(repository, nil, nil, Dependencies{})
			_, err := service.ListOwnedMappings(
				context.Background(), "homeassistant", commandTestRuntimeID, OwnedMappingPageParams{Limit: 1},
			)
			want := getFailure
			if name == "mapping read" {
				want = listFailure
			}
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}
