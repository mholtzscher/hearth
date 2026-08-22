// Package powerv1 provides the semantic schemas and Go bindings for hearth.power/v1.
package powerv1

import "github.com/mholtzscher/hearth/entitytypes"

const (
	TypeID       = "hearth.power/v1"
	OperationSet = "set"
)

type State bool

type StateSupport struct{}
type SetSupport struct{}

type OperationSupport struct {
	Set SetSupport `json:"set"`
}

type Support struct {
	State      StateSupport     `json:"state"`
	Operations OperationSupport `json:"operations"`
}

type SetParameters struct {
	Value bool `json:"value"`
}

type Codecs struct {
	State         *entitytypes.JSONCodec[State]
	Support       *entitytypes.JSONCodec[Support]
	SetParameters *entitytypes.JSONCodec[SetParameters]
}
