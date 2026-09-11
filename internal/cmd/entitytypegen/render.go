package main

// render builds every output for one Entity type: the generated files in the
// type's own package plus its SDK adapter facade and facade test. Each renderer
// owns its destination and returns unformatted Go; generateRoot runs
// formatGeneratedOutputs over the collected outputs before writing them.
func render(model entityTypeModel, modulePath string) ([]output, error) {
	types, err := renderTypes(model)
	if err != nil {
		return nil, err
	}
	return []output{
		types,
		renderCodecs(model),
		renderBehavior(model),
		renderConformanceTest(model, modulePath),
		renderFacade(model, modulePath),
		renderFacadeConformanceTest(model, modulePath),
	}, nil
}
