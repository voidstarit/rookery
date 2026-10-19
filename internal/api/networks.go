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

func broadcastIPv4(ipnet *net.IPNet) net.IP {
	ip := ipnet.IP.To4()
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^ipnet.Mask[i]
	}
	return out
}

func (s *Server) vppBlocked() (bool, string) {
	status := s.readiness.Status()
	if status.VPP.Ready {
		return false, ""
	}
	return true, "VPP is not ready: " + componentReason(status.VPP.ComponentStatus)
}

func (s *Server) rollbackBridgeDomain(bdID uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	if err := s.networks.DeleteBridgeDomain(ctx, bdID); err != nil {
		s.logger.Error("rollback bridge domain failed", "bd_id", bdID, "error", err)
	}
}
