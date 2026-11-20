package hap

import (
	"net/http"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/log"
)

func (srv *Server) getAccessories(res http.ResponseWriter, req *http.Request) {
	if !srv.IsAuthorized(req) {
		log.Info.Printf("request from %s not authorized\n", req.RemoteAddr)
		JsonError(res, JsonStatusInsufficientPrivileges)
		return
	}

	var as []*accessory.A
	as = append(as, srv.a)
	as = append(as, srv.as[:]...)

	p := struct {
		Accessories []*accessory.A `json:"accessories"`
	}{as}

	log.Debug.Println(toJSON(p))
	JsonOK(res, p)
}
