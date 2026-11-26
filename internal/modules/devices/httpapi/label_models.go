package httpapi

// LabelBody is a household Label.
type LabelBody struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"       enum:"area,tag"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// LabelRefBody names a Label assigned to a Device or Entity.
type LabelRefBody struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// EntityAreaBody is an Entity's effective Area and where it came from.
type EntityAreaBody struct {
	// Huma uses this ignored marker to emit EntityAreaBody as an object-or-null schema.
	_      struct{} `json:"-"      nullable:"true"`
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Source string   `json:"source"                 enum:"device,entity" doc:"device when inherited from the Device; entity when set on the Entity"`
}

// DeviceAreaBody is the Area assigned directly to a Device.
type DeviceAreaBody struct {
	// Huma uses this ignored marker to emit DeviceAreaBody as an object-or-null schema.
	_    struct{} `json:"-"    nullable:"true"`
	ID   string   `json:"id"`
	Name string   `json:"name"`
}
