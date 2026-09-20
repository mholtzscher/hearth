package zwavejs //nolint:testpackage // Fuzz invariants exercise private upstream DTO decoders.

import (
	"encoding/json"
	"testing"
)

// FuzzZWaveJSSnapshot protects snapshot decoding and planning from malformed
// upstream JSON. It fails if hostile input panics or produces a registration
// outside Hearth's one-to-64 Entity bound.
func FuzzZWaveJSSnapshot(fuzz *testing.F) {
	fuzz.Add([]byte(`{"state":{"controller":{"homeId":439041101},"nodes":[]}}`))
	fuzz.Add([]byte(
		`{"state":{"controller":{"homeId":439041101},"nodes":[` +
			`{"nodeId":23,"ready":true,"status":4,"interviewStage":"Complete",` +
			`"isListening":true,"endpoints":[{"index":0}],"values":[` +
			`{"commandClass":37,"property":"currentValue",` +
			`"metadata":{"type":"boolean","readable":true},"value":false},` +
			`{"commandClass":37,"property":"targetValue",` +
			`"metadata":{"type":"boolean","writeable":true}}]}]}}`,
	))

	fuzz.Fuzz(func(t *testing.T, payload []byte) {
		var snapshot networkSnapshot
		if err := json.Unmarshal(payload, &snapshot); err != nil {
			return
		}
		plan := planNetwork(0x1a2b3c4d, snapshot)
		for _, node := range plan.Nodes {
			if len(node.Plans) == 0 || len(node.Plans) > maximumPlannedEntitiesPerNode {
				t.Fatalf("planned %d Entities for node %d", len(node.Plans), node.NodeID)
			}
			if len(node.Registration.Entities) != len(node.Plans) {
				t.Fatalf("registration has %d Entities for %d plans", len(node.Registration.Entities), len(node.Plans))
			}
			for index, entity := range node.Registration.Entities {
				if entity.Key != node.Plans[index].Key || entity.ExternalID != node.Plans[index].ExternalID {
					t.Fatalf("registration Entity %d disagrees with its plan", index)
				}
			}
		}
	})
}

// FuzzZWaveJSEvent protects event-envelope and value-argument decoding from
// malformed upstream JSON. It fails if accepted value arguments lose their
// positive Command Class or string property identity.
func FuzzZWaveJSEvent(fuzz *testing.F) {
	fuzz.Add([]byte(
		`{"type":"event","event":{"source":"node","event":"value updated",` +
			`"nodeId":23,"args":{"commandClass":37,"property":"currentValue","newValue":true}}}`,
	))
	fuzz.Add([]byte(`{"type":"event","event":{"source":"controller","event":"node added","node":{"nodeId":23}}}`))

	fuzz.Fuzz(func(t *testing.T, payload []byte) {
		var event serverEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return
		}
		if err := validateServerEvent(&event); err != nil {
			return
		}
		if event.Event.Event != eventValueAdded && event.Event.Event != eventValueUpdated {
			return
		}
		args, ok := decodeValueEventArgs(event.Event.Args)
		if !ok {
			return
		}
		if args.CommandClass <= 0 || args.Property.Numeric || args.Property.Name == "" {
			t.Fatalf("accepted unusable value arguments: %#v", args)
		}
	})
}
