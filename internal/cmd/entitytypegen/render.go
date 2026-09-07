package main

import "path/filepath"

func render(model entityTypeModel, modulePath string) ([]output, error) {
	typesSource, err := renderTypes(model)
	if err != nil {
		return nil, err
	}
	codecsSource, err := renderCodecs(model)
	if err != nil {
		return nil, err
	}
	behaviorSource, err := renderBehavior(model)
	if err != nil {
		return nil, err
	}
	conformanceSource, err := renderConformanceTest(model, modulePath)
	if err != nil {
		return nil, err
	}
	facadeSource, err := renderFacade(model, modulePath)
	if err != nil {
		return nil, err
	}
	facadeTest, err := renderFacadeConformanceTest(model)
	if err != nil {
		return nil, err
	}
	return []output{
		{path: filepath.Join(model.Directory, "zz_generated_types.go"), content: typesSource},
		{path: filepath.Join(model.Directory, "zz_generated_codecs.go"), content: codecsSource},
		{path: filepath.Join(model.Directory, "zz_generated_behavior.go"), content: behaviorSource},
		{path: filepath.Join(model.Directory, "zz_generated_conformance_test.go"), content: conformanceSource},
		{
			path:    filepath.Join(model.ModuleRoot, "sdk", "adapter", model.Package, "zz_generated_facade.go"),
			content: facadeSource,
		},
		facadeTest,
	}, nil
}
