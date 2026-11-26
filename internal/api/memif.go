package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

type memifDeleterByName interface {
	DeleteMemifByName(ctx context.Context, hostName string) error
}

// memifSocketPath returns <memifDir>/<vnfID>.sock, defaulting the directory to
// /run/rookery/memif when the server has none configured.
func (s *Server) memifSocketPath(vnfID int64) string {
	dir := strings.TrimSpace(s.memifDir)
	if dir == "" {
		dir = "/run/rookery/memif"
	}
	return filepath.Join(dir, fmt.Sprintf("%d.sock", vnfID))
}

// vnfInterfaceName returns the VNF's trimmed name, or vnf<ID> when the name is
// empty.
func vnfInterfaceName(vnf store.VNF) string {
	if name := strings.TrimSpace(vnf.Name); name != "" {
		return name
	}
	return fmt.Sprintf("vnf%d", vnf.ID)
}

// vnfMemifDeleteName returns the recorded memif host interface name, falling
// back to vnf<ID> when none was stored.
func vnfMemifDeleteName(vnf store.VNF) string {
	if name := strings.TrimSpace(vnf.MemifHostName); name != "" {
		return name
	}
	return fmt.Sprintf("vnf%d", vnf.ID)
}

// activateMemifVNF starts a DHCPv4 VNF on a userspace memif interface. The
// netstack owns the DHCP service address; there is no extra gateway tap.
func (s *Server) activateMemifVNF(ctx context.Context, vnf store.VNF, network store.Network) (store.VNF, error) {
	socketPath := s.memifSocketPath(vnf.ID)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return vnf, fmt.Errorf("create memif socket directory: %w", err)
	}
	// Remove a stale socket file from a previous run or an old memif-dir.
	_ = os.Remove(socketPath)

	hostIf := vnfInterfaceName(vnf)
	memifSwIfIndex, err := s.networks.CreateMemif(
		ctx, hostIf, socketPath, uint32(vnf.ID), host.VNFMemifMAC(vnf.ID), network.BridgeDomainID,
	)
	if err != nil {
		return vnf, fmt.Errorf("create VNF memif: %w", err)
	}
	if err := s.bindNetworkInterface(ctx, network, memifSwIfIndex); err != nil {
		_ = s.networks.DeleteMemif(ctx, memifSwIfIndex)
		return vnf, err
	}
	if err := s.vnfs.StartMemif(ctx, vnf, socketPath, uint32(vnf.ID)); err != nil {
		_ = s.networks.DeleteMemif(ctx, memifSwIfIndex)
		return vnf, err
	}

	vnf.MemifSwIfIndex = memifSwIfIndex
	vnf.MemifSocket = socketPath
	vnf.MemifHostName = hostIf
	vnf.TapSwIfIndex = 0
	vnf.TapHostName = ""
	updated, err := s.persistVNFActivation(ctx, vnf, "", "", false)
	if err != nil {
		_ = s.vnfs.Stop(ctx, vnf, false)
		_ = s.networks.DeleteMemif(ctx, memifSwIfIndex)
		return vnf, err
	}
	s.logger.Info().
		Str("vnf", updated.Name).
		Str("type", updated.Type).
		Str("memif", socketPath).
		Msg("VNF activated on memif")
	return updated, nil
}

// deleteMemif removes a VNF memif by host interface name when the runtime
// supports name deletion, otherwise by swIfIndex; it is a no-op without a
// network runtime or any identifier.
func (s *Server) deleteMemif(ctx context.Context, swIfIndex uint32, hostName string) error {
	if s.networks == nil {
		return nil
	}
	if swIfIndex == 0 && strings.TrimSpace(hostName) == "" {
		return nil
	}
	if hostName != "" {
		if deleter, ok := s.networks.(memifDeleterByName); ok {
			return deleter.DeleteMemifByName(ctx, hostName)
		}
	}
	return s.networks.DeleteMemif(ctx, swIfIndex)
}
