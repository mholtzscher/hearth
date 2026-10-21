package httpapi

type DestinationBody struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}
