package mcpapi_test

type greetInput struct {
	Name string `json:"name" jsonschema:"the name to greet"`
}

type greetOutput struct {
	Greeting string `json:"greeting"`
}

type noInput struct{}
