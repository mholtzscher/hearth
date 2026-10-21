//nolint:gochecknoglobals // The process cache owns only immutable contracts and preparation errors.
package mcpapi

import (
	"reflect"
	"sync"
)

// Separate maps make direction part of the key. Entries contain only immutable
// contracts or preparation errors; failed preparation is also performed once.
var defaultInputs sync.Map
var defaultOutputs sync.Map

func defaultInput[I any]() (*InputContract[I], error) {
	entry, _ := defaultInputs.LoadOrStore(reflect.TypeFor[I](), sync.OnceValues(func() (*InputContract[I], error) {
		return PrepareInput[I](nil)
	}))
	prepare, ok := entry.(func() (*InputContract[I], error))
	if !ok {
		panic("input contract cache type mismatch")
	}
	return prepare()
}

func defaultOutput[O any]() (*OutputContract[O], error) {
	entry, _ := defaultOutputs.LoadOrStore(reflect.TypeFor[O](), sync.OnceValues(func() (*OutputContract[O], error) {
		return PrepareOutput[O](nil)
	}))
	prepare, ok := entry.(func() (*OutputContract[O], error))
	if !ok {
		panic("output contract cache type mismatch")
	}
	return prepare()
}
