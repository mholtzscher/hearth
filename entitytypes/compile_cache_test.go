package entitytypes_test

import (
	"sync"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes"
	"github.com/mholtzscher/hearth/entitytypes/powerv1"
)

func TestCompileIsolatesWrappers(t *testing.T) {
	t.Parallel()
	first, err := powerv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	second, err := powerv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	*first.State = entitytypes.JSONCodec[powerv1.State]{}
	*first.Support = entitytypes.JSONCodec[powerv1.Support]{}
	*first.SetParameters = entitytypes.JSONCodec[powerv1.SetParameters]{}
	first.State = nil
	checkPowerCodecs(t, second)
	third, err := powerv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	checkPowerCodecs(t, third)
}

func TestConcurrentCompileAndDecode(t *testing.T) {
	t.Parallel()
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			codecs, err := powerv1.Compile()
			if err != nil {
				t.Error(err)
				return
			}
			checkPowerCodecs(t, codecs)
		})
	}
	workers.Wait()
}

func checkPowerCodecs(t *testing.T, codecs *powerv1.Codecs) {
	t.Helper()
	if _, _, err := codecs.State.Decode([]byte(`true`)); err != nil {
		t.Errorf("valid state: %v", err)
	}
	if _, _, err := codecs.State.Decode([]byte(`"on"`)); err == nil {
		t.Error("invalid state accepted")
	}
	if _, _, err := codecs.Support.Decode([]byte(`{"state":{},"operations":{"set":{}}}`)); err != nil {
		t.Errorf("valid support: %v", err)
	}
	if _, _, err := codecs.Support.Decode(
		[]byte(`{"state":{},"operations":{"set":{}},"unexpected":true}`),
	); err == nil {
		t.Error("invalid support accepted")
	}
	if _, _, err := codecs.SetParameters.Decode([]byte(`{"value":true}`)); err != nil {
		t.Errorf("valid parameters: %v", err)
	}
	if _, _, err := codecs.SetParameters.Decode([]byte(`{"value":"on"}`)); err == nil {
		t.Error("invalid parameters accepted")
	}
}

func BenchmarkCompileCached(b *testing.B) {
	if _, err := powerv1.Compile(); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := powerv1.Compile(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileFresh(b *testing.B) {
	files := powerv1.SchemaFiles()
	state, err := powerv1.FS.ReadFile(files[powerv1.StateSchemaID])
	if err != nil {
		b.Fatal(err)
	}
	support, err := powerv1.FS.ReadFile(files[powerv1.SupportSchemaID])
	if err != nil {
		b.Fatal(err)
	}
	parameters, err := powerv1.FS.ReadFile(files[powerv1.SetParametersSchemaID])
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, compileErr := entitytypes.CompileJSONCodec[powerv1.State](
			powerv1.StateSchemaID,
			state,
			nil,
		); compileErr != nil {
			b.Fatal(compileErr)
		}
		if _, compileErr := entitytypes.CompileJSONCodec[powerv1.Support](
			powerv1.SupportSchemaID,
			support,
			nil,
		); compileErr != nil {
			b.Fatal(compileErr)
		}
		if _, compileErr := entitytypes.CompileJSONCodec[powerv1.SetParameters](
			powerv1.SetParametersSchemaID,
			parameters,
			nil,
		); compileErr != nil {
			b.Fatal(compileErr)
		}
	}
}
