package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	sdkTestEntityID      = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	sdkTestWrongEntityID = "ent_6ba7b810-9b21-11d1-80b4-00c04fd430c8"
)

func renderFacadeConformanceTest(model entityTypeModel, modulePath string) output {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)
	// The import block is declared in full and pruned by formatting, so a
	// renderer never maintains conditional import lists by hand.
	source.WriteString("import (\n")
	source.WriteString("\t\"context\"\n")
	source.WriteString("\t\"encoding/json\"\n")
	source.WriteString("\t\"testing\"\n")
	source.WriteString("\t\"time\"\n\n")
	source.WriteString("\t\"github.com/mholtzscher/hearth/sdk/adapter\"\n")
	source.WriteString("\t\"github.com/mholtzscher/hearth/sdk/adapter/adaptertest\"\n")
	fmt.Fprintf(&source, "\t%s\n", strconv.Quote(modulePath+"/internal/entitytypetest"))
	source.WriteString(")\n\n")
	if model.Stateless {
		writeStatelessOmissionTest(&source, model)
	} else {
		writeObservationConformanceTest(&source, model)
		writeObservationMetadataTest(&source, model)
		writeObservationValidationTest(&source, model)
	}
	writeEntityDescriptorTest(&source, model)
	if model.EventSource {
		writeEntityEventFacadeConformanceTest(&source, model)
	}
	if len(model.Operations) > 0 {
		writeCommandConformanceTest(&source, model)
		writeCommandInvalidSupportTest(&source, model)
	}
	return output{
		path:    filepath.Join(model.ModuleRoot, "sdk", "adapter", model.Package, "zz_generated_facade_test.go"),
		content: []byte(source.String()),
	}
}

// writeStatelessOmissionTest marks the stateless policy in the generated
// facade test: stateless types carry no State observations, so the facade
// defines no ObservationInput or NewObservation and no observation
// conformance runs. The generator regression test pins the omission by
// asserting the rendered facade and facade test never reference either
// symbol; this marker keeps the policy visible at the conformance seam.
func writeStatelessOmissionTest(source *strings.Builder, model entityTypeModel) {
	source.WriteString("func TestGeneratedStatelessOmitsObservation(t *testing.T) {\n")
	fmt.Fprintf(
		source,
		"\tt.Log(%s)\n",
		strconv.Quote("stateless "+model.TypeID+" defines no ObservationInput or NewObservation"),
	)
	source.WriteString("}\n\n")
}

func writeObservationConformanceTest(source *strings.Builder, model entityTypeModel) {
	source.WriteString("func TestGeneratedObservationConformance(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { t.Fatal(err) }\n")
	for _, example := range model.Examples.Cases {
		fmt.Fprintf(source, "\tt.Run(%s, func(t *testing.T) {\n", strconv.Quote(example.Name))
		fmt.Fprintf(
			source,
			"\t\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
			rawQuote(example.Support),
		)
		source.WriteString("\t\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
		for index, state := range example.States {
			source.WriteString("\t\t{\n")
			fmt.Fprintf(
				source,
				"\t\t\traw := json.RawMessage(%s)\n",
				rawQuote(state.Value),
			)
			source.WriteString("\t\t\tstate, _, codecErr := codecs.State.Decode(raw)\n")
			if state.Valid {
				source.WriteString("\t\t\tif codecErr != nil {\n")
				fmt.Fprintf(
					source,
					"\t\t\t\tt.Errorf(\"State example %d marked valid but rejected by State codec: %%v\", codecErr)\n",
					index+1,
				)
				source.WriteString("\t\t\t}\n")
				source.WriteString("\t\t\tif codecErr == nil {\n")
				fmt.Fprintf(
					source,
					"\t\t\t\tif _, observationErr := NewObservation(ObservationInput{EntityID: %s, Support: support, State: state, AdapterReceivedAt: time.Unix(0, 0).UTC()}); observationErr != nil {\n",
					strconv.Quote(sdkTestEntityID),
				)
				fmt.Fprintf(
					source,
					"\t\t\t\t\tt.Errorf(\"State example %d marked valid but rejected by Observation constructor: %%v\", observationErr)\n",
					index+1,
				)
				source.WriteString("\t\t\t\t}\n")
				source.WriteString("\t\t\t}\n")
			} else {
				source.WriteString("\t\t\tvar plainState State\n")
				source.WriteString("\t\t\tplainErr := json.Unmarshal(raw, &plainState)\n")
				source.WriteString("\t\t\tif codecErr == nil {\n")
				source.WriteString("\t\t\t\tif _, observationErr := NewObservation(ObservationInput{EntityID: ")
				fmt.Fprintf(
					source,
					"%s, Support: support, State: state, AdapterReceivedAt: time.Unix(0, 0).UTC()}); observationErr == nil {\n",
					strconv.Quote(sdkTestEntityID),
				)
				fmt.Fprintf(
					source,
					"\t\t\t\t\tt.Errorf(\"State example %d marked invalid was accepted by Observation constructor\")\n",
					index+1,
				)
				source.WriteString("\t\t\t\t} else {\n")
				writeRequireValidationError(source, "\t\t\t\t\t", "reject support-incompatible State")
				source.WriteString("\t\t\t\t}\n")
				source.WriteString("\t\t\t} else if plainErr == nil {\n")
				source.WriteString("\t\t\t\t// Schema-invalid but Go-representable values must reach the\n")
				source.WriteString("\t\t\t\t// Observation constructor through ordinary JSON and be\n")
				source.WriteString("\t\t\t\t// rejected there. Wire-only values that Go cannot represent\n")
				source.WriteString("\t\t\t\t// stay in contract-test territory. Ordinary JSON construction\n")
				source.WriteString("\t\t\t\t// may drop authored invalidity (a missing required field\n")
				source.WriteString("\t\t\t\t// becomes a valid zero-filled field); only inputs that survive\n")
				source.WriteString("\t\t\t\t// an ordinary JSON roundtrip carry a constructor claim, judged\n")
				source.WriteString("\t\t\t\t// without production validation.\n")
				source.WriteString(
					"\t\t\t\tif entitytypetest.CanonicalValue(t, plainState) == " +
						"entitytypetest.CanonicalJSON(t, raw) {\n",
				)
				fmt.Fprintf(
					source,
					"\t\t\t\t\tif _, observationErr := NewObservation(ObservationInput{EntityID: %s, Support: support, State: plainState, AdapterReceivedAt: time.Unix(0, 0).UTC()}); observationErr == nil {\n",
					strconv.Quote(sdkTestEntityID),
				)
				fmt.Fprintf(
					source,
					"\t\t\t\t\t\tt.Errorf(\"typed-invalid State example %d was accepted by Observation constructor\")\n",
					index+1,
				)
				source.WriteString("\t\t\t\t\t} else {\n")
				writeRequireValidationError(source, "\t\t\t\t\t\t", "reject typed-invalid State")
				source.WriteString("\t\t\t\t\t}\n")
				source.WriteString("\t\t\t\t}\n")
				source.WriteString("\t\t\t}\n")
			}
			source.WriteString("\t\t}\n")
		}
		source.WriteString("\t})\n")
	}
	source.WriteString("}\n\n")
}

// writeEntityEventFacadeConformanceTest emits the event-source SDK probe: the
// typed builder accepts a supported name, rejects an unsupported or
// non-canonical name, rejects a missing Entity identity, and rejects support
// that no longer satisfies its schema.
func writeEntityEventFacadeConformanceTest(source *strings.Builder, model entityTypeModel) {
	names, err := entityEventNamesFromSupport(model.Examples.Cases[0].Support)
	if err != nil {
		panic("event-source examples lost their names: " + err.Error())
	}
	unsupported := unsupportedEntityEventName(names)
	source.WriteString("func TestGeneratedEntityEventConformance(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { t.Fatal(err) }\n")
	fmt.Fprintf(
		source,
		"\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
		rawQuote(model.Examples.Cases[0].Support),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
	fmt.Fprintf(
		source,
		"\tif len(support.Events.Names) != %d { t.Fatalf(\"Entity Event names = %%v\", support.Events.Names) }\n",
		len(names),
	)
	source.WriteString("\tsupportedName := string(support.Events.Names[0])\n")
	source.WriteString(
		"\tevent, err := NewEntityEvent(EntityEventInput{EntityID: " + strconv.Quote(sdkTestEntityID) +
			", Support: support, Name: supportedName})\n",
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"Entity Event: %v\", err) }\n")
	fmt.Fprintf(
		source,
		"\tif event.EntityID != %s || event.Name != supportedName { t.Errorf(\"Entity Event = %%+v\", event) }\n",
		strconv.Quote(sdkTestEntityID),
	)
	fmt.Fprintf(
		source,
		"\tif _, err := NewEntityEvent(EntityEventInput{EntityID: %s, Support: support, Name: %s}); err == nil {\n",
		strconv.Quote(sdkTestEntityID),
		strconv.Quote(unsupported),
	)
	source.WriteString("\t\tt.Error(\"unsupported Entity Event name was accepted\")\n\t} else {\n")
	writeAdapterValidationError(source, "\t\t", "err", "reject unsupported Entity Event name")
	source.WriteString("\t}\n")
	fmt.Fprintf(
		source,
		"\tif _, err := NewEntityEvent(EntityEventInput{EntityID: %s, Support: support, Name: \"not a name\"}); err == nil {\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\t\tt.Error(\"non-canonical Entity Event name was accepted\")\n\t} else {\n")
	writeAdapterValidationError(source, "\t\t", "err", "reject non-canonical Entity Event name")
	source.WriteString("\t}\n")
	source.WriteString(
		"\tif _, err := NewEntityEvent(EntityEventInput{Support: support, Name: supportedName}); err == nil {\n",
	)
	source.WriteString("\t\tt.Error(\"Entity Event without an entity ID was accepted\")\n\t} else {\n")
	writeAdapterValidationError(source, "\t\t", "err", "reject empty entity ID")
	source.WriteString("\t}\n")
	source.WriteString("\tinvalidSupport := support\n\tinvalidSupport.Events.Names = nil\n")
	fmt.Fprintf(
		source,
		"\tif _, err := NewEntityEvent(EntityEventInput{EntityID: %s, Support: invalidSupport, Name: supportedName}); err == nil {\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\t\tt.Error(\"Entity Event with invalid Entity support was accepted\")\n\t} else {\n")
	writeAdapterValidationError(source, "\t\t", "err", "reject invalid Entity support")
	source.WriteString("\t}\n")
	source.WriteString("}\n\n")
}

func writeRequireValidationError(source *strings.Builder, indent, action string) {
	writeAdapterValidationError(source, indent, "observationErr", action)
}

func writeAdapterValidationError(source *strings.Builder, indent, errorVariable, action string) {
	fmt.Fprintf(
		source,
		"%sadaptertest.RequireValidationError(t, %s, %s)\n",
		indent,
		errorVariable,
		strconv.Quote(action),
	)
}

func firstValidState(model entityTypeModel) (exampleCase, validityExample) {
	for _, example := range model.Examples.Cases {
		for _, state := range example.States {
			if state.Valid {
				return example, state
			}
		}
	}
	return model.Examples.Cases[0], model.Examples.Cases[0].States[0]
}

func writeObservationMetadataTest(source *strings.Builder, model entityTypeModel) {
	example, state := firstValidState(model)
	source.WriteString("func TestGeneratedObservationMetadata(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { t.Fatal(err) }\n")
	fmt.Fprintf(
		source,
		"\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
		rawQuote(example.Support),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
	fmt.Fprintf(
		source,
		"\tstate, _, err := codecs.State.Decode(json.RawMessage(%s))\n",
		rawQuote(state.Value),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"State: %v\", err) }\n")
	source.WriteString("\treceivedAt := time.Date(2026, time.March, 10, 14, 30, 0,\n")
	source.WriteString("\t\t123456789, time.FixedZone(\"CET\", 3600))\n")
	fmt.Fprintf(
		source,
		"\tobservation, err := NewObservation(ObservationInput{EntityID: %s, Support: support, State: state, AdapterReceivedAt: receivedAt})\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"observation: %v\", err) }\n")
	fmt.Fprintf(
		source,
		"\tif observation.EntityID != %s { t.Errorf(\"observation entity ID = %%q\", observation.EntityID) }\n",
		strconv.Quote(sdkTestEntityID),
	)
	fmt.Fprintf(
		source,
		"\tif entitytypetest.CanonicalJSON(t, observation.Value) != entitytypetest.CanonicalJSON(t, json.RawMessage(%s)) { t.Errorf(\"observation value = %%s\", observation.Value) }\n",
		rawQuote(state.Value),
	)
	source.WriteString(
		"\tif received := adaptertest.ParseAdapterTime(t, \"adapter received time\", observation.AdapterReceivedAt); !received.Equal(receivedAt) {\n",
	)
	source.WriteString(
		"\t\tt.Errorf(\"adapter received time = %s, want %s\", observation.AdapterReceivedAt, receivedAt.UTC().Format(time.RFC3339Nano))\n",
	)
	source.WriteString("\t}\n")
	source.WriteString("\tif observation.SourceUpdatedAt != nil {\n")
	source.WriteString(
		"\t\tt.Errorf(\"observation without source time carries SourceUpdatedAt = %s\", *observation.SourceUpdatedAt)\n",
	)
	source.WriteString("\t}\n")
	source.WriteString("\tsourceAt := time.Date(2026, time.March, 9, 8, 15, 30, 0, time.FixedZone(\"EST\", -5*3600))\n")
	fmt.Fprintf(
		source,
		"\tsourced, err := NewObservation(ObservationInput{EntityID: %s, Support: support, State: state, AdapterReceivedAt: receivedAt, SourceUpdatedAt: &sourceAt})\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"sourced observation: %v\", err) }\n")
	source.WriteString(
		"\tif sourced.SourceUpdatedAt == nil { t.Fatal(\"observation with source time has no SourceUpdatedAt\") }\n",
	)
	source.WriteString(
		"\tif updated := adaptertest.ParseAdapterTime(t, \"source updated time\", *sourced.SourceUpdatedAt); !updated.Equal(sourceAt) {\n",
	)
	source.WriteString(
		"\t\tt.Errorf(\"source updated time = %s, want %s\", *sourced.SourceUpdatedAt, sourceAt.UTC().Format(time.RFC3339Nano))\n",
	)
	source.WriteString("\t}\n")
	source.WriteString("}\n\n")
}

func writeObservationValidationTest(source *strings.Builder, model entityTypeModel) {
	example, state := firstValidState(model)
	source.WriteString("func TestGeneratedObservationValidation(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { t.Fatal(err) }\n")
	fmt.Fprintf(
		source,
		"\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
		rawQuote(example.Support),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
	fmt.Fprintf(
		source,
		"\tstate, _, err := codecs.State.Decode(json.RawMessage(%s))\n",
		rawQuote(state.Value),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"State: %v\", err) }\n")
	source.WriteString("\treceivedAt := time.Date(2026, time.March, 10, 14, 30, 0, 0, time.UTC)\n")
	source.WriteString(
		"\tif _, err := NewObservation(ObservationInput{Support: support, State: state, AdapterReceivedAt: receivedAt}); err == nil {\n",
	)
	source.WriteString("\t\tt.Error(\"Observation without entity ID was accepted\")\n")
	source.WriteString("\t} else {\n")
	source.WriteString("\t\tadaptertest.RequireValidationError(t, err, \"reject empty entity ID\")\n")
	source.WriteString("\t}\n")
	fmt.Fprintf(
		source,
		"\tif _, err := NewObservation(ObservationInput{EntityID: %s, Support: support, State: state}); err == nil {\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\t\tt.Error(\"Observation without adapter received time was accepted\")\n")
	source.WriteString("\t} else {\n")
	source.WriteString("\t\tadaptertest.RequireValidationError(t, err, \"reject zero adapter received time\")\n")
	source.WriteString("\t}\n")
	source.WriteString("\tzeroSource := time.Time{}\n")
	fmt.Fprintf(
		source,
		"\tif _, err := NewObservation(ObservationInput{EntityID: %s, Support: support, State: state, AdapterReceivedAt: receivedAt, SourceUpdatedAt: &zeroSource}); err == nil {\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\t\tt.Error(\"Observation with zero source updated time was accepted\")\n")
	source.WriteString("\t} else {\n")
	source.WriteString("\t\tadaptertest.RequireValidationError(t, err, \"reject zero source updated time\")\n")
	source.WriteString("\t}\n")
	if mutation, ok := invalidSupportMutation(model); ok {
		fmt.Fprintf(
			source,
			"\tinvalidSupport := support\n\tinvalidSupport.%s = %s\n",
			mutation.Selector,
			mutation.Expression,
		)
		fmt.Fprintf(
			source,
			"\tif _, err := NewObservation(ObservationInput{EntityID: %s, Support: invalidSupport, State: state, AdapterReceivedAt: receivedAt}); err == nil {\n",
			strconv.Quote(sdkTestEntityID),
		)
		source.WriteString("\t\tt.Error(\"Observation with invalid Entity support was accepted\")\n")
		source.WriteString("\t} else {\n")
		source.WriteString("\t\tadaptertest.RequireValidationError(t, err, \"reject invalid Entity support\")\n")
		source.WriteString("\t}\n")
	}
	writeEmptySupportObservationCheck(source, model)
	source.WriteString("}\n\n")
}

func writeEntityDescriptorTest(source *strings.Builder, model entityTypeModel) {
	example, _ := firstValidState(model)
	source.WriteString("func TestGeneratedEntityDescriptor(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { t.Fatal(err) }\n")
	fmt.Fprintf(
		source,
		"\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
		rawQuote(example.Support),
	)
	source.WriteString("\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
	source.WriteString(
		"\tmetadata := adapter.EntityMetadata{Key: \"key_01890f47-7a6b-7c4d-8e9f-0123456789ab\", ExternalID: \"ext-001\", Name: \"generated entity\"}\n",
	)
	source.WriteString("\tdescriptor, err := NewEntityDescriptor(metadata, support)\n")
	source.WriteString("\tif err != nil { t.Fatalf(\"descriptor: %v\", err) }\n")
	source.WriteString("\tif descriptor.Key != metadata.Key { t.Errorf(\"descriptor key = %q\", descriptor.Key) }\n")
	source.WriteString(
		"\tif descriptor.ExternalID != metadata.ExternalID { t.Errorf(\"descriptor external ID = %q\", descriptor.ExternalID) }\n",
	)
	source.WriteString(
		"\tif descriptor.Name != metadata.Name { t.Errorf(\"descriptor name = %q\", descriptor.Name) }\n",
	)
	fmt.Fprintf(
		source,
		"\tif descriptor.Type != %s { t.Errorf(\"descriptor type = %%q\", descriptor.Type) }\n",
		strconv.Quote(model.TypeID),
	)
	fmt.Fprintf(
		source,
		"\tif entitytypetest.CanonicalJSON(t, descriptor.Support) != entitytypetest.CanonicalJSON(t, json.RawMessage(%s)) { t.Errorf(\"descriptor support = %%s\", descriptor.Support) }\n",
		rawQuote(example.Support),
	)
	if mutation, ok := invalidSupportMutation(model); ok {
		fmt.Fprintf(
			source,
			"\tinvalidSupport := support\n\tinvalidSupport.%s = %s\n",
			mutation.Selector,
			mutation.Expression,
		)
		source.WriteString("\tif _, err := NewEntityDescriptor(metadata, invalidSupport); err == nil {\n")
		source.WriteString("\t\tt.Error(\"descriptor with invalid Entity support was accepted\")\n")
		source.WriteString("\t} else {\n")
		source.WriteString("\t\tadaptertest.RequireValidationError(t, err, \"reject invalid Entity support\")\n")
		source.WriteString("\t}\n")
	}
	writeEmptySupportDescriptorCheck(source, model)
	writeDescriptorInvalidSupports(source, model)
	source.WriteString("}\n\n")
}

// writeDescriptorInvalidSupports proves that descriptor construction rejects
// schema-decodable supports violating support_validation rules (for example
// a numericsetting minimum above its maximum). The mutation check above only
// covers schema-invalid values; these independently authored examples cover
// relational invalidity.
func writeDescriptorInvalidSupports(source *strings.Builder, model entityTypeModel) {
	for index, raw := range model.Examples.InvalidSupports {
		source.WriteString("\t{\n")
		fmt.Fprintf(
			source,
			"\t\tinvalidSupport%d, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
			index+1,
			rawQuote(raw),
		)
		fmt.Fprintf(
			source,
			"\t\tif err != nil { t.Fatalf(\"invalid support %d: %%v\", err) }\n",
			index+1,
		)
		fmt.Fprintf(
			source,
			"\t\tif _, err := NewEntityDescriptor(metadata, invalidSupport%d); err == nil {\n",
			index+1,
		)
		fmt.Fprintf(
			source,
			"\t\t\tt.Error(\"descriptor with invalid Entity support %d was accepted\")\n",
			index+1,
		)
		source.WriteString("\t\t} else {\n")
		fmt.Fprintf(
			source,
			"\t\t\tadaptertest.RequireValidationError(t, err, \"reject invalid Entity support %d\")\n",
			index+1,
		)
		source.WriteString("\t\t}\n\t}\n")
	}
}

// writeEmptySupportDescriptorCheck proves a descriptor cannot be built from the
// Go zero value of a support type whose schema fixes a required string. It is
// emitted only when the schema guarantees Support{} is schema-invalid.
func writeEmptySupportDescriptorCheck(source *strings.Builder, model entityTypeModel) {
	if !supportZeroValueRejected(model) {
		return
	}
	source.WriteString("\tvar emptySupport Support\n")
	source.WriteString("\tif _, err := NewEntityDescriptor(metadata, emptySupport); err == nil {\n")
	source.WriteString("\t\tt.Error(\"descriptor with empty Entity support was accepted\")\n")
	source.WriteString("\t} else {\n")
	source.WriteString("\t\tadaptertest.RequireValidationError(t, err, \"reject empty Entity support\")\n")
	source.WriteString("\t}\n")
}

// writeEmptySupportObservationCheck proves an observation cannot be built from
// the Go zero value of a support type whose schema fixes a required string. It
// is emitted only when the schema guarantees Support{} is schema-invalid.
func writeEmptySupportObservationCheck(source *strings.Builder, model entityTypeModel) {
	if !supportZeroValueRejected(model) {
		return
	}
	source.WriteString("\tvar emptySupport Support\n")
	fmt.Fprintf(
		source,
		"\tif _, err := NewObservation(ObservationInput{EntityID: %s, Support: emptySupport, State: state, AdapterReceivedAt: receivedAt}); err == nil {\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\t\tt.Error(\"Observation with empty Entity support was accepted\")\n")
	source.WriteString("\t} else {\n")
	source.WriteString("\t\tadaptertest.RequireValidationError(t, err, \"reject empty Entity support\")\n")
	source.WriteString("\t}\n")
}

// supportMutation names one required support field and the schema-invalid Go
// expression generated facade tests assign to it. Expression is already a
// rendered Go literal so callers never re-encode kind-specific values.
type supportMutation struct {
	Selector   string
	Expression string
}

// findSupportLeafMutation finds the first required support leaf for which
// leafMutation yields a mutation, descending through required object properties
// in property order. It carries the accumulating Go field selector into
// selector so a leaf predicate never builds selectors itself.
func findSupportLeafMutation(
	schema schemaNode,
	selector string,
	leafMutation func(schemaNode, string) (supportMutation, bool),
) (supportMutation, bool) {
	if schema.Type != schemaTypeObject {
		return leafMutation(schema, selector)
	}
	for _, name := range sortedProperties(schema.Properties) {
		if !required(schema, name) {
			continue
		}
		field, err := exportedName(name)
		if err != nil {
			continue
		}
		childSelector := field
		if selector != "" {
			childSelector = selector + "." + field
		}
		if mutation, ok := findSupportLeafMutation(
			schema.Properties[name],
			childSelector,
			leafMutation,
		); ok {
			return mutation, true
		}
	}
	return supportMutation{}, false
}

// invalidSupportMutation finds a required support leaf that can be mutated to a
// schema-invalid value and returns the rendered mutation. String consts win over
// bounded integers across the whole schema: a fixed support value is the most
// faithful thing for generated tests to prove immutable, and an appended-suffix
// string plus an out-of-range bounded integer are both schema-invalid yet
// ordinary JSON can represent them. Types whose support schemas accept every
// Go-representable required value report false; their invalid-support coverage
// is impossible to construct.
func invalidSupportMutation(model entityTypeModel) (supportMutation, bool) {
	for _, leafMutation := range []func(schemaNode, string) (supportMutation, bool){
		stringConstLeafMutation,
		integerLeafMutation,
	} {
		if mutation, ok := findSupportLeafMutation(model.SupportSchema, "", leafMutation); ok {
			return mutation, true
		}
	}
	return supportMutation{}, false
}

// stringConstLeafMutation returns a rendered mutation for a required string
// leaf carrying an authoritative const. A literal with an appended suffix is a
// deterministic Go string that always differs from the const, so the schema
// rejects it regardless of any other constraint.
func stringConstLeafMutation(schema schemaNode, selector string) (supportMutation, bool) {
	if schema.Type != string(kindString) || len(schema.Const) == 0 {
		return supportMutation{}, false
	}
	var constValue string
	if err := json.Unmarshal(schema.Const, &constValue); err != nil {
		return supportMutation{}, false
	}
	return supportMutation{
		Selector:   selector,
		Expression: strconv.Quote(constValue + "-invalid"),
	}, true
}

// integerLeafMutation returns a rendered mutation for a required integer leaf
// with an inclusive schema range that cannot accept some representable int64.
func integerLeafMutation(schema schemaNode, selector string) (supportMutation, bool) {
	if schema.Type != string(kindInteger) || schema.Minimum == nil || schema.Maximum == nil {
		return supportMutation{}, false
	}
	value, ok := invalidIntegerMutation(schema.Minimum.String(), schema.Maximum.String())
	if !ok {
		return supportMutation{}, false
	}
	return supportMutation{Selector: selector, Expression: strconv.FormatInt(value, 10)}, true
}

// supportZeroValueRejected reports whether the Go zero value of the generated
// Support type violates the support schema, so generated facade tests can prove
// required support cannot be omitted. A required string const with a non-empty
// value guarantees this: the generated field is a non-pointer Go string, so
// Support{} marshals the empty string, which no non-empty const accepts.
func supportZeroValueRejected(model entityTypeModel) bool {
	_, ok := findSupportLeafMutation(model.SupportSchema, "", zeroValueStringConstLeaf)
	return ok
}

// zeroValueStringConstLeaf reports a required string const whose non-empty value
// rejects the Go zero value. Only the leaf predicate result matters here, so it
// returns no selector or expression.
func zeroValueStringConstLeaf(schema schemaNode, _ string) (supportMutation, bool) {
	if schema.Type != string(kindString) || len(schema.Const) == 0 {
		return supportMutation{}, false
	}
	var constValue string
	if json.Unmarshal(schema.Const, &constValue) != nil || constValue == "" {
		return supportMutation{}, false
	}
	return supportMutation{}, true
}

// invalidIntegerMutation returns a representable int64 outside an inclusive
// integer schema range, including ranges whose JSON bounds are fractional.
// It returns no mutation when the range contains every representable int64.
func invalidIntegerMutation(minimumRaw, maximumRaw string) (int64, bool) {
	minimum, minimumOK := new(big.Rat).SetString(minimumRaw)
	maximum, maximumOK := new(big.Rat).SetString(maximumRaw)
	if !minimumOK || !maximumOK {
		return 0, false
	}
	minInt64 := big.NewInt(math.MinInt64)
	maxInt64 := big.NewInt(math.MaxInt64)
	if maximum.Cmp(new(big.Rat).SetInt(minInt64)) < 0 {
		return math.MinInt64, true
	}
	if minimum.Cmp(new(big.Rat).SetInt(maxInt64)) > 0 {
		return math.MaxInt64, true
	}

	above := floorRationalInteger(maximum)
	above.Add(above, big.NewInt(1))
	if above.Cmp(maxInt64) <= 0 {
		return above.Int64(), true
	}

	below := ceilRationalInteger(minimum)
	below.Sub(below, big.NewInt(1))
	if below.Cmp(minInt64) >= 0 {
		return below.Int64(), true
	}
	return 0, false
}

func floorRationalInteger(value *big.Rat) *big.Int {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	if value.Sign() < 0 && remainder.Sign() != 0 {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}

func ceilRationalInteger(value *big.Rat) *big.Int {
	negated := new(big.Rat).Neg(value)
	floor := floorRationalInteger(negated)
	return floor.Neg(floor)
}

// unknownOperationCandidate returns a deterministic operation name absent from
// every declared operation, so the generated unknown operation routing check
// cannot collide with a real operation name.
func unknownOperationCandidate(operations []operationModel) string {
	names := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		names[operation.Name] = struct{}{}
	}
	for _, candidate := range []string{"unknown", "unknown-operation"} {
		if _, exists := names[candidate]; !exists {
			return candidate
		}
	}
	for index := 2; ; index++ {
		candidate := "unknown-operation-" + strconv.Itoa(index)
		if _, exists := names[candidate]; !exists {
			return candidate
		}
	}
}

//nolint:gocognit // Generated command cases are assembled in one deterministic pass.
func writeCommandConformanceTest(source *strings.Builder, model entityTypeModel) {
	unknownOperation := unknownOperationCandidate(model.Operations)
	source.WriteString("func TestGeneratedCommandConformance(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { t.Fatal(err) }\n")
	for _, example := range model.Examples.Cases {
		fmt.Fprintf(source, "\tt.Run(%s, func(t *testing.T) {\n", strconv.Quote(example.Name))
		fmt.Fprintf(
			source,
			"\t\tsupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
			rawQuote(example.Support),
		)
		source.WriteString("\t\tif err != nil { t.Fatalf(\"support: %v\", err) }\n")
		supported := make([]operationModel, 0, len(model.Operations))
		for _, operation := range model.Operations {
			if _, ok := example.Operations[operation.Name]; ok {
				supported = append(supported, operation)
			}
		}
		if len(supported) == 0 {
			writeNoSupportedOperationCheck(source, "\t\t")
			for _, operation := range model.Operations {
				writeAbsentOperationCheck(source, operation, supported, "\t\t", false)
			}
			source.WriteString("\t})\n")
			continue
		}
		writeCommandHandlers(source, supported, "\t\t")
		source.WriteString("\t\tdeadline := time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)\n")
		for _, operation := range supported {
			writeValidCommandCheck(source, example, operation, "\t\t")
		}
		for _, operation := range supported {
			writeInvalidParametersChecks(source, example, operation, "\t\t")
		}
		writeRoutingChecks(source, example, supported, unknownOperation, "\t\t")
		for _, operation := range supported {
			writeMissingHandlerCheck(source, supported, operation, "\t\t")
		}
		for _, operation := range model.Operations {
			if _, ok := example.Operations[operation.Name]; ok {
				continue
			}
			writeAbsentOperationCheck(source, operation, supported, "\t\t", true)
		}
		if mutation, ok := invalidSupportMutation(model); ok {
			writeInvalidSupportCommandCheck(source, supported, mutation, "\t\t")
		}
		source.WriteString("\t})\n")
	}
	source.WriteString("}\n")
}

// writeCommandHandlers declares one recording handler per supported operation and
// constructs the facade command handler under test.
func writeCommandHandlers(
	source *strings.Builder,
	supported []operationModel,
	indent string,
) {
	for _, operation := range supported {
		fmt.Fprintf(
			source,
			"%s%sCalls := []%sCommand{}\n",
			indent,
			lowerFirst(operation.GoName),
			operation.GoName,
		)
	}
	fmt.Fprintf(
		source,
		"%shandler, err := NewCommandHandler(%s, support, Handlers{\n",
		indent,
		strconv.Quote(sdkTestEntityID),
	)
	for _, operation := range supported {
		fmt.Fprintf(
			source,
			"%s\t%s: func(_ context.Context, command %sCommand, _ adapter.Responder) error {\n",
			indent,
			operation.GoName,
			operation.GoName,
		)
		fmt.Fprintf(
			source,
			"%s\t\t%sCalls = append(%sCalls, command)\n",
			indent,
			lowerFirst(operation.GoName),
			lowerFirst(operation.GoName),
		)
		fmt.Fprintf(source, "%s\t\treturn nil\n%s\t},\n", indent, indent)
	}
	source.WriteString(indent + "})\n")
	source.WriteString(indent + "if err != nil { t.Fatalf(\"command handler: %v\", err) }\n")
}

func writeValidCommandCheck(source *strings.Builder, example exampleCase, operation operationModel, indent string) {
	parameters := firstValidParameters(example, operation)
	fmt.Fprintf(
		source,
		"%sif err := handler(context.Background(), adapter.Command{ID: \"cmd_%s_one\", CorrelationID: \"cor_%s_one\", EntityID: %s, OperationName: %s, Parameters: json.RawMessage(%s), Deadline: deadline.Format(time.RFC3339Nano)}, nil); err != nil {\n",
		indent,
		operation.Name,
		operation.Name,
		strconv.Quote(sdkTestEntityID),
		strconv.Quote(operation.Name),
		rawQuote(parameters.Value),
	)
	fmt.Fprintf(
		source,
		"%s\tt.Fatalf(\"%s command: %%v\", err)\n%s}\n",
		indent,
		operation.Name,
		indent,
	)
	fmt.Fprintf(
		source,
		"%sif len(%sCalls) != 1 { t.Fatalf(\"%s handler calls = %%d, want 1\", len(%sCalls)) }\n",
		indent,
		lowerFirst(operation.GoName),
		operation.Name,
		lowerFirst(operation.GoName),
	)
	fmt.Fprintf(
		source,
		"%sreceived%s := %sCalls[0]\n",
		indent,
		operation.GoName,
		lowerFirst(operation.GoName),
	)
	fmt.Fprintf(
		source,
		"%sif received%s.ID != \"cmd_%s_one\" || received%s.CorrelationID != \"cor_%s_one\" || received%s.EntityID != %s {\n",
		indent,
		operation.GoName,
		operation.Name,
		operation.GoName,
		operation.Name,
		operation.GoName,
		strconv.Quote(sdkTestEntityID),
	)
	fmt.Fprintf(
		source,
		"%s\tt.Errorf(\"%s command identity = %%+v\", received%s)\n%s}\n",
		indent,
		operation.Name,
		operation.GoName,
		indent,
	)
	fmt.Fprintf(
		source,
		"%sif !received%s.Deadline.Equal(deadline) { t.Errorf(\"%s command deadline = %%v\", received%s.Deadline) }\n",
		indent,
		operation.GoName,
		operation.Name,
		operation.GoName,
	)
	fmt.Fprintf(
		source,
		"%sif entitytypetest.CanonicalValue(t, received%s.Parameters) != entitytypetest.CanonicalJSON(t, json.RawMessage(%s)) { t.Errorf(\"%s command parameters = %%+v\", received%s.Parameters) }\n",
		indent,
		operation.GoName,
		rawQuote(parameters.Value),
		operation.Name,
		operation.GoName,
	)
}

func firstValidParameters(example exampleCase, operation operationModel) validityExample {
	for _, parameters := range example.Operations[operation.Name].Parameters {
		if parameters.Valid {
			return parameters
		}
	}
	return example.Operations[operation.Name].Parameters[0]
}

func writeInvalidParametersChecks(
	source *strings.Builder,
	example exampleCase,
	operation operationModel,
	indent string,
) {
	for index, parameters := range example.Operations[operation.Name].Parameters {
		if parameters.Valid {
			source.WriteString(indent + "{\n")
			fmt.Fprintf(
				source,
				"%s\tif _, _, decodeErr := codecs.%sParameters.Decode(json.RawMessage(%s)); decodeErr != nil {\n",
				indent,
				operation.GoName,
				rawQuote(parameters.Value),
			)
			fmt.Fprintf(
				source,
				"%s\t\tt.Errorf(\"%s parameter example %d marked valid but rejected by parameters codec: %%v\", decodeErr)\n",
				indent,
				operation.Name,
				index+1,
			)
			source.WriteString(indent + "\t}\n")
			source.WriteString(indent + "}\n")
			continue
		}
		source.WriteString(indent + "{\n")
		fmt.Fprintf(
			source,
			"%s\traw := json.RawMessage(%s)\n",
			indent,
			rawQuote(parameters.Value),
		)
		fmt.Fprintf(
			source,
			"%s\tbefore := len(%sCalls)\n",
			indent,
			lowerFirst(operation.GoName),
		)
		fmt.Fprintf(
			source,
			"%s\tif invokeErr := handler(context.Background(), adapter.Command{ID: \"cmd_%s_invalid_%d\", CorrelationID: \"cor_%s_invalid_%d\", EntityID: %s, OperationName: %s, Parameters: raw, Deadline: deadline.Format(time.RFC3339Nano)}, nil); invokeErr == nil {\n",
			indent,
			operation.Name,
			index+1,
			operation.Name,
			index+1,
			strconv.Quote(sdkTestEntityID),
			strconv.Quote(operation.Name),
		)
		fmt.Fprintf(
			source,
			"%s\t\tt.Errorf(\"invalid %s parameter example %d was accepted\")\n",
			indent,
			operation.Name,
			index+1,
		)
		source.WriteString(indent + "\t}\n")
		fmt.Fprintf(
			source,
			"%s\tif len(%sCalls) != before {\n",
			indent,
			lowerFirst(operation.GoName),
		)
		fmt.Fprintf(
			source,
			"%s\t\tt.Errorf(\"%s handler invoked for invalid %s parameter example %d\")\n",
			indent,
			operation.Name,
			operation.Name,
			index+1,
		)
		source.WriteString(indent + "\t}\n")
		source.WriteString(indent + "}\n")
	}
}

func writeRoutingChecks(
	source *strings.Builder,
	example exampleCase,
	supported []operationModel,
	unknownOperation string,
	indent string,
) {
	operation := supported[0]
	parameters := firstValidParameters(example, operation)
	source.WriteString(indent + "{\n")
	fmt.Fprintf(
		source,
		"%s\tbefore := %s\n",
		indent,
		routingCallTotal(supported),
	)
	fmt.Fprintf(
		source,
		"%s\tif invokeErr := handler(context.Background(), adapter.Command{ID: \"cmd_wrong_entity\", CorrelationID: \"cor_wrong_entity\", EntityID: %s, OperationName: %s, Parameters: json.RawMessage(%s), Deadline: deadline.Format(time.RFC3339Nano)}, nil); invokeErr == nil {\n",
		indent,
		strconv.Quote(sdkTestWrongEntityID),
		strconv.Quote(operation.Name),
		rawQuote(parameters.Value),
	)
	source.WriteString(indent + "\t\tt.Error(\"command for wrong entity ID was accepted\")\n")
	source.WriteString(indent + "\t}\n")
	fmt.Fprintf(
		source,
		"%s\tif invokeErr := handler(context.Background(), adapter.Command{ID: \"cmd_unknown_operation\", CorrelationID: \"cor_unknown_operation\", EntityID: %s, OperationName: %s, Parameters: json.RawMessage(%s), Deadline: deadline.Format(time.RFC3339Nano)}, nil); invokeErr == nil {\n",
		indent,
		strconv.Quote(sdkTestEntityID),
		strconv.Quote(unknownOperation),
		rawQuote(parameters.Value),
	)
	source.WriteString(indent + "\t\tt.Error(\"command for unknown operation was accepted\")\n")
	source.WriteString(indent + "\t}\n")
	fmt.Fprintf(
		source,
		"%s\tif total := %s; total != before {\n",
		indent,
		routingCallTotal(supported),
	)
	source.WriteString(
		indent + "\t\tt.Errorf(\"misrouted command invoked a handler: calls = %d, want %d\", total, before)\n",
	)
	source.WriteString(indent + "\t}\n")
	source.WriteString(indent + "}\n")
}

func routingCallTotal(supported []operationModel) string {
	parts := make([]string, 0, len(supported))
	for _, operation := range supported {
		parts = append(parts, "len("+lowerFirst(operation.GoName)+"Calls)")
	}
	return strings.Join(parts, " + ")
}

// writeInvalidSupportCommandCheck proves that command construction rejects
// schema-invalid Entity support before any routing occurs.
func writeInvalidSupportCommandCheck(
	source *strings.Builder,
	supported []operationModel,
	mutation supportMutation,
	indent string,
) {
	fmt.Fprintf(
		source,
		"%sinvalidSupport := support\n%sinvalidSupport.%s = %s\n",
		indent,
		indent,
		mutation.Selector,
		mutation.Expression,
	)
	fmt.Fprintf(
		source,
		"%sif _, err := NewCommandHandler(%s, invalidSupport, Handlers{\n",
		indent,
		strconv.Quote(sdkTestEntityID),
	)
	for _, operation := range supported {
		fmt.Fprintf(
			source,
			"%s\t%s: func(_ context.Context, _ %sCommand, _ adapter.Responder) error { return nil },\n",
			indent,
			operation.GoName,
			operation.GoName,
		)
	}
	source.WriteString(indent + "}); err == nil {\n")
	source.WriteString(indent + "\tt.Error(\"command handler with invalid Entity support was accepted\")\n")
	source.WriteString(indent + "} else {\n")
	source.WriteString(indent + "\tadaptertest.RequireValidationError(t, err, \"reject invalid Entity support\")\n")
	source.WriteString(indent + "}\n")
}

// writeCommandInvalidSupportTest proves that command construction rejects
// schema-decodable supports violating support_validation rules. Handlers
// match each invalid support's enabled operations so only support validity
// decides construction. Types without authored invalid supports emit no test:
// their ValidateSupport accepts every schema-valid value by construction.
func writeCommandInvalidSupportTest(source *strings.Builder, model entityTypeModel) {
	if len(model.Examples.InvalidSupports) == 0 {
		return
	}
	source.WriteString("\nfunc TestGeneratedCommandInvalidSupport(t *testing.T) {\n")
	source.WriteString("\tcodecs, err := codecs()\n\tif err != nil { t.Fatal(err) }\n")
	for index, raw := range model.Examples.InvalidSupports {
		source.WriteString("\t{\n")
		fmt.Fprintf(
			source,
			"\t\tinvalidSupport, _, err := codecs.Support.Decode(json.RawMessage(%s))\n",
			rawQuote(raw),
		)
		fmt.Fprintf(
			source,
			"\t\tif err != nil { t.Fatalf(\"invalid support %d: %%v\", err) }\n",
			index+1,
		)
		fmt.Fprintf(
			source,
			"\t\tif _, err := NewCommandHandler(%s, invalidSupport, Handlers{\n",
			strconv.Quote(sdkTestEntityID),
		)
		for _, operation := range invalidSupportOperations(model, raw) {
			fmt.Fprintf(
				source,
				"\t\t\t%s: func(_ context.Context, _ %sCommand, _ adapter.Responder) error { return nil },\n",
				operation.GoName,
				operation.GoName,
			)
		}
		source.WriteString("\t\t}); err == nil {\n")
		fmt.Fprintf(
			source,
			"\t\t\tt.Error(\"command handler with invalid Entity support %d was accepted\")\n",
			index+1,
		)
		source.WriteString("\t\t} else {\n")
		fmt.Fprintf(
			source,
			"\t\t\tadaptertest.RequireValidationError(t, err, \"reject invalid Entity support %d\")\n",
			index+1,
		)
		source.WriteString("\t\t}\n\t}\n")
	}
	source.WriteString("}\n")
}

// invalidSupportOperations returns the declared operations enabled by an
// authored invalid_support value in manifest order. Required operations are
// always included; optional operations only when the value enables them.
func invalidSupportOperations(model entityTypeModel, raw json.RawMessage) []operationModel {
	var shape struct {
		Operations map[string]json.RawMessage `json:"operations"`
	}
	enabled := make(map[string]struct{})
	if err := json.Unmarshal(raw, &shape); err == nil {
		for name := range shape.Operations {
			enabled[name] = struct{}{}
		}
	}
	operations := make([]operationModel, 0, len(model.Operations))
	for _, operation := range model.Operations {
		if _, ok := enabled[operation.Name]; ok || operation.Required {
			operations = append(operations, operation)
		}
	}
	return operations
}

// writeNoSupportedOperationCheck proves that a support value with no enabled
// operations still requires construction to fail with a validation error.
func writeNoSupportedOperationCheck(source *strings.Builder, indent string) {
	fmt.Fprintf(
		source,
		"%sif _, err := NewCommandHandler(%s, support, Handlers{}); err == nil {\n",
		indent,
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString(indent + "\tt.Error(\"command handler with no supported operations was accepted\")\n")
	source.WriteString(indent + "} else {\n")
	source.WriteString(indent + "\tadaptertest.RequireValidationError(t, err, ")
	source.WriteString("\"reject command handler with no supported operations\")\n")
	source.WriteString(indent + "}\n")
}

// writeMissingHandlerCheck proves that omitting one supported handler fails
// construction, covering required handlers and present optional handlers alike.
func writeMissingHandlerCheck(
	source *strings.Builder,
	supported []operationModel,
	operation operationModel,
	indent string,
) {
	fmt.Fprintf(
		source,
		"%sif _, err := NewCommandHandler(%s, support, Handlers{\n",
		indent,
		strconv.Quote(sdkTestEntityID),
	)
	for _, other := range supported {
		if other.Name == operation.Name {
			continue
		}
		fmt.Fprintf(
			source,
			"%s\t%s: func(_ context.Context, _ %sCommand, _ adapter.Responder) error { return nil },\n",
			indent,
			other.GoName,
			other.GoName,
		)
	}
	source.WriteString(indent + "}); err == nil {\n")
	fmt.Fprintf(
		source,
		"%s\tt.Error(\"command handler without %s handler was accepted\")\n",
		indent,
		operation.Name,
	)
	source.WriteString(indent + "} else {\n")
	fmt.Fprintf(
		source,
		"%s\tadaptertest.RequireValidationError(t, err, \"reject command handler without %s handler\")\n",
		indent,
		operation.Name,
	)
	source.WriteString(indent + "}\n")
}

// writeAbsentOperationCheck proves that an unsupported optional operation
// rejects a provided handler and, when a handler is constructible without it,
// is not routable.
func writeAbsentOperationCheck(
	source *strings.Builder,
	target operationModel,
	supported []operationModel,
	indent string,
	withRouting bool,
) {
	fmt.Fprintf(
		source,
		"%sif _, err := NewCommandHandler(%s, support, Handlers{\n",
		indent,
		strconv.Quote(sdkTestEntityID),
	)
	for _, operation := range supported {
		fmt.Fprintf(
			source,
			"%s\t%s: func(_ context.Context, command %sCommand, _ adapter.Responder) error {\n",
			indent,
			operation.GoName,
			operation.GoName,
		)
		fmt.Fprintf(
			source,
			"%s\t\t%sCalls = append(%sCalls, command)\n",
			indent,
			lowerFirst(operation.GoName),
			lowerFirst(operation.GoName),
		)
		fmt.Fprintf(source, "%s\t\treturn nil\n%s\t},\n", indent, indent)
	}
	fmt.Fprintf(
		source,
		"%s\t%s: func(_ context.Context, _ %sCommand, _ adapter.Responder) error { return nil },\n",
		indent,
		target.GoName,
		target.GoName,
	)
	source.WriteString(indent + "}); err == nil {\n")
	fmt.Fprintf(
		source,
		"%s\tt.Error(\"command handler for unsupported %s operation was accepted\")\n",
		indent,
		target.Name,
	)
	source.WriteString(indent + "} else {\n")
	fmt.Fprintf(
		source,
		"%s\tadaptertest.RequireValidationError(t, err, \"reject command handler for unsupported %s operation\")\n",
		indent,
		target.Name,
	)
	source.WriteString(indent + "}\n")
	if !withRouting {
		return
	}
	source.WriteString(indent + "{\n")
	fmt.Fprintf(
		source,
		"%s\tbefore := %s\n",
		indent,
		routingCallTotal(supported),
	)
	fmt.Fprintf(
		source,
		"%s\tif invokeErr := handler(context.Background(), adapter.Command{ID: \"cmd_%s_absent\", CorrelationID: \"cor_%s_absent\", EntityID: %s, OperationName: %s, Parameters: json.RawMessage(`null`), Deadline: deadline.Format(time.RFC3339Nano)}, nil); invokeErr == nil {\n",
		indent,
		target.Name,
		target.Name,
		strconv.Quote(sdkTestEntityID),
		strconv.Quote(target.Name),
	)
	fmt.Fprintf(
		source,
		"%s\t\tt.Error(\"command for unsupported %s operation was accepted\")\n",
		indent,
		target.Name,
	)
	source.WriteString(indent + "\t}\n")
	fmt.Fprintf(
		source,
		"%s\tif total := %s; total != before {\n",
		indent,
		routingCallTotal(supported),
	)
	source.WriteString(
		indent + "\t\tt.Errorf(\"misrouted command invoked a handler: calls = %d, want %d\", total, before)\n",
	)
	source.WriteString(indent + "\t}\n")
	source.WriteString(indent + "}\n")
}
