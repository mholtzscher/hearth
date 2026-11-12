package levoit

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"maps"
	"strings"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func bindingKey(cid string) string {
	digest := sha256.Sum256([]byte(cid))
	return "levoit-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:]))
}

func (a *Adapter) loadMappings(ctx context.Context) error {
	const mappingPageSize = 200
	request := adapter.OwnedMappingPageRequest{Limit: mappingPageSize}
	for {
		page, err := a.session.ListOwnedMappings(ctx, request)
		if err != nil {
			return err
		}
		for _, mapping := range page.Items {
			runtime := a.devices[mapping.BindingKey]
			if runtime == nil {
				runtime = &deviceRuntime{gate: make(chan struct{}, 1), ids: make(map[string]string)}
				a.devices[mapping.BindingKey] = runtime
			}
			runtime.ids[mapping.EntityKey] = mapping.EntityID
		}
		if page.NextCursor == "" {
			return nil
		}
		request.Cursor = page.NextCursor
	}
}

func (a *Adapter) register(ctx context.Context, runtime *deviceRuntime, d device) error {
	plans, err := a.entities(d)
	if err != nil {
		return err
	}
	registration := adapter.Registration{BindingKey: bindingKey(d.CID),
		Device: adapter.DeviceDescriptor{ExternalID: &d.CID, Name: d.Name, Kind: "air-purifier"}}
	for _, plan := range plans {
		registration.Entities = append(registration.Entities, plan.descriptor)
	}
	binding, err := a.session.Register(ctx, registration)
	if err != nil {
		return err
	}
	if binding.BindingKey != registration.BindingKey || len(binding.Entities) != len(plans) {
		return errors.New("purifier registration mapping mismatch")
	}
	ids := make(map[string]string, len(plans))
	for _, entity := range binding.Entities {
		if entity.EntityID == "" || ids[entity.Key] != "" {
			return errors.New("purifier registration mapping mismatch")
		}
		ids[entity.Key] = entity.EntityID
	}
	routes := make(map[string]adapter.CommandHandler)
	for _, plan := range plans {
		id := ids[plan.descriptor.Key]
		if id == "" {
			return errors.New("purifier registration mapping mismatch")
		}
		if plan.handler != nil {
			handler, handlerErr := plan.handler(id, runtime)
			if handlerErr != nil {
				return handlerErr
			}
			routes[id] = handler
		}
	}
	runtime.device, runtime.ids, runtime.plans = d, ids, plans
	a.mu.Lock()
	runtime.active = true
	maps.Copy(a.routes, routes)
	a.mu.Unlock()
	return nil
}
