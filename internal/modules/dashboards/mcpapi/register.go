// Package mcpapi publishes dashboard presentation without saved-dashboard writes.
package mcpapi

import mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"

func Register(server *mcptools.Server, presenter Presenter) {
	registerPresentView(server, &handler{presenter: presenter})
}
