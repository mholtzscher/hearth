package devices

// ReadingKind names what a dashboard can read from an Entity type's State.
type ReadingKind string

// ReadingQuantity is a scalar numeric State with a canonical unit.
const ReadingQuantity ReadingKind = "quantity"

// Reading is the generated presentation contract for one Entity type,
// declared by its manifest. Units are canonical UCUM codes; conversion and the
// household's display choice belong to presentation layers. FixedUnit and
// SupportUnitPath are mutually exclusive.
type Reading struct {
	Kind            ReadingKind
	ValueType       string // "integer" or "number", from the State schema
	FixedUnit       string
	SupportUnitPath string
}
