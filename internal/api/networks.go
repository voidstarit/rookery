package api

import (
	"context"
	"net"
	"time"
)

const (
	maxNetworkNameLength = 64
	rollbackTimeout      = 5 * time.Second
)

// broadcastIPv4 returns the broadcast address of ipnet by setting the host bits
// of its IPv4 network address.
func broadcastIPv4(ipnet *net.IPNet) net.IP {
	ip := ipnet.IP.To4()
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^ipnet.Mask[i]
	}
	return out
}

// vppBlocked reports whether VPP is not ready and, when blocked, a message
// naming the readiness component's detail or state.
func (s *Server) vppBlocked() (bool, string) {
	status := s.readiness.Status()
	if status.VPP.Ready {
		return false, ""
	}
	return true, "VPP is not ready: " + componentReason(status.VPP.ComponentStatus)
}

// rollbackBridgeDomain deletes a bridge domain with a short, detached timeout so
// a failed network create can clean up after a request is cancelled; failures
// are only logged.
func (s *Server) rollbackBridgeDomain(bdID uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	if err := s.networks.DeleteBridgeDomain(ctx, bdID); err != nil {
		s.logger.Error().Uint32("bd_id", bdID).Err(err).Msg("rollback bridge domain failed")
	}
}
