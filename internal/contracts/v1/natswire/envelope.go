package natswire

type Envelope[T any] struct {
	ID            string  `json:"id"`
	Schema        string  `json:"schema"`
	EmittedAt     string  `json:"emitted_at"`
	CorrelationID string  `json:"correlation_id"`
	CausationID   *string `json:"causation_id,omitempty"`
	Data          T       `json:"data"`
}
