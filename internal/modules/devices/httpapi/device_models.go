package httpapi

type DeviceBody struct {
	AdapterName  string  `json:"adapter_name"`
	NameOverride *string `json:"name_override" nullable:"true"`
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
}
