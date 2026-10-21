package mcpapi

import (
	"sync"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

//nolint:gochecknoglobals // Immutable canonical codec, compiled once per transport.
var definitionCodec = sync.OnceValues(automations.NewDefinitionCodec)
