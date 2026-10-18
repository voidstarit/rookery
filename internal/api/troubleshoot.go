package api

import "net/http"

func (s *Server) handleVPPNodeGraph(w http.ResponseWriter, r *http.Request) {
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}
	graph, err := s.networks.VPPNodeGraph(r.Context())
	if err != nil {
		s.logger.Error("read VPP node graph failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read VPP node graph: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, graph)
}

func (s *Server) handleACLInterfaceCounters(w http.ResponseWriter, r *http.Request) {
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}
	counters, err := s.networks.ACLInterfaceCounters(r.Context())
	if err != nil {
		s.logger.Error("read ACL interface counters failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read ACL interface counters: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, counters)
}
