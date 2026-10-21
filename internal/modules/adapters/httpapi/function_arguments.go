package httpapi

type FunctionPageInput struct {
	AdapterID string `path:"adapter_id"`
	Limit     int    `                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor    string `                  query:"cursor"`
}
type FunctionInput struct {
	AdapterID   string `path:"adapter_id"`
	FunctionKey string `path:"function_key"`
}
