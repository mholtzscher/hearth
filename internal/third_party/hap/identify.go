package hap

import (
	"net/http"

	"github.com/brutella/hap/log"
)

func (srv *Server) identify(res http.ResponseWriter, req *http.Request) {
	if srv.IsPaired() {
		log.Info.Printf("request only valid if unpaired")
		JsonError(res, JsonStatusInsufficientPrivileges)
		return
	}

	if srv.a.IdentifyFunc != nil {
		srv.a.IdentifyFunc(req)
	}

	res.WriteHeader(http.StatusNoContent)
}
