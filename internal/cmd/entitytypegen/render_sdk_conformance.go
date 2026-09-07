package main

import (
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

func renderFacadeConformanceTest(model entityTypeModel) (output, error) {
	var source strings.Builder
	generatedHeader(&source)
	fmt.Fprintf(&source, "package %s\n\n", model.Package)
	source.WriteString("import (\n")
	if len(model.Operations) > 0 {
		source.WriteString("\t\"context\"\n")
	}
	source.WriteString("\t\"bytes\"\n")
	source.WriteString("\t\"encoding/json\"\n")
	source.WriteString("\t\"errors\"\n")
	source.WriteString("\t\"math/big\"\n")
	source.WriteString("\t\"sort\"\n")
	source.WriteString("\t\"strconv\"\n")
	source.WriteString("\t\"testing\"\n")
	source.WriteString("\t\"time\"\n\n")
	source.WriteString("\t\"github.com/mholtzscher/hearth/sdk/adapter\"\n")
	source.WriteString(")\n\n")
	writeSDKTestHelpers(&source)
	writeCanonicalJSONRegressionTest(&source)
	writeObservationConformanceTest(&source, model)
	writeObservationMetadataTest(&source, model)
	writeObservationValidationTest(&source, model)
	writeEntityDescriptorTest(&source, model)
	if len(model.Operations) > 0 {
		writeCommandConformanceTest(&source, model)
	}
	formatted, err := formatGenerated(source.String())
	if err != nil {
		return output{}, err
	}
	return output{
		path:    filepath.Join(model.ModuleRoot, "sdk", "adapter", model.Package, "zz_generated_facade_test.go"),
		content: formatted,
	}, nil
}

func writeSDKTestHelpers(source *strings.Builder) {
	source.WriteString("func requireValidationError(t *testing.T, err error, action string) {\n")
	source.WriteString("\tt.Helper()\n")
	source.WriteString("\tvar validationErr *adapter.ValidationError\n")
	source.WriteString("\tif err == nil || !errors.As(err, &validationErr) {\n")
	source.WriteString("\t\tt.Fatalf(\"%s: expected adapter validation error, got %v\", action, err)\n")
	source.WriteString("\t}\n")
	source.WriteString("}\n\n")
	writeCanonicalJSONHelpers(source)
	writeCanonicalValueHelper(source)
	writeParseAdapterTimeHelper(source)
}

func writeCanonicalJSONHelpers(source *strings.Builder) {
	source.WriteString("func canonicalJSONValue(value any) string {\n")
	source.WriteString("\tswitch value := value.(type) {\n")
	source.WriteString("\tcase nil:\n\t\treturn \"null\"\n")
	source.WriteString("\tcase bool:\n\t\tif value { return \"bool:true\" }; return \"bool:false\"\n")
	source.WriteString("\tcase json.Number:\n")
	source.WriteString("\t\trational, ok := new(big.Rat).SetString(value.String())\n")
	source.WriteString("\t\tif !ok { return \"number:\" + value.String() }\n")
	source.WriteString("\t\treturn \"number:\" + rational.RatString()\n")
	source.WriteString("\tcase string:\n\t\treturn \"string:\" + strconv.Quote(value)\n")
	source.WriteString("\tcase []any:\n")
	source.WriteString("\t\tcanonical := \"array:[\"\n")
	source.WriteString("\t\tfor index, item := range value {\n")
	source.WriteString("\t\t\tif index > 0 { canonical += \",\" }\n")
	source.WriteString("\t\t\tcanonical += canonicalJSONValue(item)\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t\treturn canonical + \"]\"\n")
	source.WriteString("\tcase map[string]any:\n")
	source.WriteString("\t\tkeys := make([]string, 0, len(value))\n")
	source.WriteString("\t\tfor key := range value { keys = append(keys, key) }\n")
	source.WriteString("\t\tsort.Strings(keys)\n")
	source.WriteString("\t\tcanonical := \"object:{\"\n")
	source.WriteString("\t\tfor index, key := range keys {\n")
	source.WriteString("\t\t\tif index > 0 { canonical += \",\" }\n")
	source.WriteString("\t\t\tcanonical += strconv.Quote(key) + \"=\" + canonicalJSONValue(value[key])\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t\treturn canonical + \"}\"\n")
	source.WriteString("\tdefault:\n\t\treturn \"unsupported\"\n")
	source.WriteString("\t}\n")
	source.WriteString("}\n\n")
	source.WriteString("func canonicalJSON(t *testing.T, raw json.RawMessage) string {\n")
	source.WriteString("\tt.Helper()\n")
	source.WriteString("\tdecoder := json.NewDecoder(bytes.NewReader(raw))\n")
	source.WriteString("\tdecoder.UseNumber()\n")
	source.WriteString("\tvar value any\n")
	source.WriteString("\tif err := decoder.Decode(&value); err != nil {\n")
	source.WriteString("\t\tt.Fatalf(\"decode JSON: %v\", err)\n")
	source.WriteString("\t}\n")
	source.WriteString("\treturn canonicalJSONValue(value)\n")
	source.WriteString("}\n\n")
}

func writeCanonicalValueHelper(source *strings.Builder) {
	source.WriteString("func canonicalValue(t *testing.T, value any) string {\n")
	source.WriteString("\tt.Helper()\n")
	source.WriteString("\traw, err := json.Marshal(value)\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\tt.Fatalf(\"encode JSON: %v\", err)\n")
	source.WriteString("\t}\n")
	source.WriteString("\treturn canonicalJSON(t, raw)\n")
	source.WriteString("}\n\n")
}

func writeParseAdapterTimeHelper(source *strings.Builder) {
	source.WriteString("func parseAdapterTime(t *testing.T, field, raw string) time.Time {\n")
	source.WriteString("\tt.Helper()\n")
	source.WriteString("\tparsed, err := time.Parse(time.RFC3339Nano, raw)\n")
	source.WriteString("\tif err != nil {\n")
	source.WriteString("\t\tt.Fatalf(\"%s %q is not RFC3339Nano: %v\", field, raw, err)\n")
	source.WriteString("\t}\n")
	source.WriteString("\tif _, offset := parsed.Zone(); offset != 0 {\n")
	source.WriteString("\t\tt.Fatalf(\"%s %q is not formatted as UTC\", field, raw)\n")
	source.WriteString("\t}\n")
	source.WriteString("\treturn parsed\n")
	source.WriteString("}\n\n")
}

func writeCanonicalJSONRegressionTest(source *strings.Builder) {
	source.WriteString("func TestGeneratedCanonicalJSONPreservesExactNumbers(t *testing.T) {\n")
	source.WriteString("\tequal := []struct{ left, right string }{\n")
	source.WriteString("\t\t{\"1\", \"1.0\"},\n")
	source.WriteString("\t\t{\"1\", \"1e0\"},\n")
	source.WriteString("\t\t{\"-0\", \"0\"},\n")
	source.WriteString("\t\t{\"{\\\"value\\\":9007199254740993}\", \"{\\\"value\\\":9.007199254740993e15}\"},\n")
	source.WriteString("\t}\n")
	source.WriteString("\tfor _, example := range equal {\n")
	source.WriteString("\t\tleft := canonicalJSON(t, json.RawMessage(example.left))\n")
	source.WriteString("\t\tright := canonicalJSON(t, json.RawMessage(example.right))\n")
	source.WriteString("\t\tif left != right {\n")
	source.WriteString("\t\t\tt.Errorf(\"canonical numbers differ: %s != %s\", left, right)\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t}\n")
	source.WriteString("\tunequal := []struct{ left, right string }{\n")
	source.WriteString("\t\t{\"9007199254740993\", \"9007199254740992\"},\n")
	source.WriteString("\t\t{\"75\", \"\\\"75\\\"\"},\n")
	source.WriteString("\t\t{\"{\\\"value\\\":1}\", \"{\\\"value\\\":\\\"1\\\"}\"},\n")
	source.WriteString("\t}\n")
	source.WriteString("\tfor _, example := range unequal {\n")
	source.WriteString("\t\tleft := canonicalJSON(t, json.RawMessage(example.left))\n")
	source.WriteString("\t\tright := canonicalJSON(t, json.RawMessage(example.right))\n")
	source.WriteString("\t\tif left == right {\n")
	source.WriteString("\t\t\tt.Errorf(\"canonical values collided: %s == %s\", left, right)\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t}\n")
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
				source.WriteString("\t\t\t\tif canonicalValue(t, plainState) == canonicalJSON(t, raw) {\n")
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

func writeRequireValidationError(source *strings.Builder, indent, action string) {
	fmt.Fprintf(source, "%srequireValidationError(t, observationErr, %s)\n", indent, strconv.Quote(action))
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
		"\tif canonicalJSON(t, observation.Value) != canonicalJSON(t, json.RawMessage(%s)) { t.Errorf(\"observation value = %%s\", observation.Value) }\n",
		rawQuote(state.Value),
	)
	source.WriteString(
		"\tif received := parseAdapterTime(t, \"adapter received time\", observation.AdapterReceivedAt); !received.Equal(receivedAt) {\n",
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
		"\tif updated := parseAdapterTime(t, \"source updated time\", *sourced.SourceUpdatedAt); !updated.Equal(sourceAt) {\n",
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
	source.WriteString("\t\trequireValidationError(t, err, \"reject empty entity ID\")\n")
	source.WriteString("\t}\n")
	fmt.Fprintf(
		source,
		"\tif _, err := NewObservation(ObservationInput{EntityID: %s, Support: support, State: state}); err == nil {\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\t\tt.Error(\"Observation without adapter received time was accepted\")\n")
	source.WriteString("\t} else {\n")
	source.WriteString("\t\trequireValidationError(t, err, \"reject zero adapter received time\")\n")
	source.WriteString("\t}\n")
	source.WriteString("\tzeroSource := time.Time{}\n")
	fmt.Fprintf(
		source,
		"\tif _, err := NewObservation(ObservationInput{EntityID: %s, Support: support, State: state, AdapterReceivedAt: receivedAt, SourceUpdatedAt: &zeroSource}); err == nil {\n",
		strconv.Quote(sdkTestEntityID),
	)
	source.WriteString("\t\tt.Error(\"Observation with zero source updated time was accepted\")\n")
	source.WriteString("\t} else {\n")
	source.WriteString("\t\trequireValidationError(t, err, \"reject zero source updated time\")\n")
	source.WriteString("\t}\n")
	if selector, value, ok := invalidSupportMutation(model); ok {
		fmt.Fprintf(
			source,
			"\tinvalidSupport := support\n\tinvalidSupport.%s = %d\n",
			selector,
			value,
		)
		fmt.Fprintf(
			source,
			"\tif _, err := NewObservation(ObservationInput{EntityID: %s, Support: invalidSupport, State: state, AdapterReceivedAt: receivedAt}); err == nil {\n",
			strconv.Quote(sdkTestEntityID),
		)
		source.WriteString("\t\tt.Error(\"Observation with invalid Entity support was accepted\")\n")
		source.WriteString("\t} else {\n")
		source.WriteString("\t\trequireValidationError(t, err, \"reject invalid Entity support\")\n")
		source.WriteString("\t}\n")
	}
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
		"\tif canonicalJSON(t, descriptor.Support) != canonicalJSON(t, json.RawMessage(%s)) { t.Errorf(\"descriptor support = %%s\", descriptor.Support) }\n",
		rawQuote(example.Support),
	)
	if selector, value, ok := invalidSupportMutation(model); ok {
		fmt.Fprintf(
			source,
			"\tinvalidSupport := support\n\tinvalidSupport.%s = %d\n",
			selector,
			value,
		)
		source.WriteString("\tif _, err := NewEntityDescriptor(metadata, invalidSupport); err == nil {\n")
		source.WriteString("\t\tt.Error(\"descriptor with invalid Entity support was accepted\")\n")
		source.WriteString("\t} else {\n")
		source.WriteString("\t\trequireValidationError(t, err, \"reject invalid Entity support\")\n")
		source.WriteString("\t}\n")
	}
	source.WriteString("}\n\n")
}

// invalidSupportMutation finds a required integer leaf in the support schema and
// returns the Go field selector and an out-of-range value that ordinary JSON can
// still represent. Types whose support schemas accept every Go-representable
// value report false; their invalid-support coverage is impossible to construct.
func invalidSupportMutation(model entityTypeModel) (string, int64, bool) {
	names := sortedProperties(model.SupportSchema.Properties)
	for _, name := range names {
		if !required(model.SupportSchema, name) {
			continue
		}
		field, err := exportedName(name)
		if err != nil {
			continue
		}
		if selector, value, ok := invalidLeaf(model.SupportSchema.Properties[name], field); ok {
			return selector, value, true
		}
	}
	return "", 0, false
}

func invalidLeaf(schema schemaNode, selector string) (string, int64, bool) {
	if schema.Type == string(kindInteger) && schema.Minimum != nil && schema.Maximum != nil {
		if value, ok := invalidIntegerMutation(schema.Minimum.String(), schema.Maximum.String()); ok {
			return selector, value, true
		}
	}
	if schema.Type != schemaTypeObject {
		return "", 0, false
	}
	for _, name := range sortedProperties(schema.Properties) {
		if !required(schema, name) {
			continue
		}
		field, err := exportedName(name)
		if err != nil {
			continue
		}
		if result, value, ok := invalidLeaf(schema.Properties[name], selector+"."+field); ok {
			return result, value, true
		}
	}
	return "", 0, false
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
		if selector, value, ok := invalidSupportMutation(model); ok {
			writeInvalidSupportCommandCheck(source, supported, selector, value, "\t\t")
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
		"%sif canonicalValue(t, received%s.Parameters) != canonicalJSON(t, json.RawMessage(%s)) { t.Errorf(\"%s command parameters = %%+v\", received%s.Parameters) }\n",
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
	selector string,
	value int64,
	indent string,
) {
	fmt.Fprintf(source, "%sinvalidSupport := support\n%sinvalidSupport.%s = %d\n", indent, indent, selector, value)
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
	source.WriteString(indent + "\trequireValidationError(t, err, \"reject invalid Entity support\")\n")
	source.WriteString(indent + "}\n")
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
	source.WriteString(indent + "\trequireValidationError(t, err, ")
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
		"%s\trequireValidationError(t, err, \"reject command handler without %s handler\")\n",
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
		"%s\trequireValidationError(t, err, \"reject command handler for unsupported %s operation\")\n",
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
