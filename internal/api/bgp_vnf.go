package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/voidstarit/rookery/internal/store"
)

// NormalizeBGPVNFConfig validates BGP AS numbers, router ID, local and peer
// addresses, transport, and MD5 password rules, returning the canonical config.
// Advertised prefixes are child rows and are validated on their own write path.
func (s *Server) NormalizeBGPVNFConfig(config store.BGPVNFConfig) (store.BGPVNFConfig, error) {
	transport := strings.ToLower(strings.TrimSpace(config.Transport))
	if transport == "" {
		transport = "memif"
	}
	if transport != "memif" && transport != "tap" {
		return store.BGPVNFConfig{}, errors.New("config.transport must be memif or tap")
	}
	if config.LocalAS == 0 {
		return store.BGPVNFConfig{}, errors.New("config.local_as is required")
	}
	if config.PeerAS == 0 {
		return store.BGPVNFConfig{}, errors.New("config.peer_as is required")
	}
	routerID := net.ParseIP(strings.TrimSpace(config.RouterID)).To4()
	if routerID == nil {
		return store.BGPVNFConfig{}, errors.New("config.router_id must be an IPv4 address")
	}
	localIP, localNet, err := net.ParseCIDR(strings.TrimSpace(config.LocalIP))
	if err != nil || localIP == nil {
		return store.BGPVNFConfig{}, errors.New("config.local_ip must be an IPv4 or IPv6 CIDR")
	}
	ipv6 := localIP.To4() == nil
	ones, bits := localNet.Mask.Size()
	if (ipv6 && bits != 128) || (!ipv6 && bits != 32) || ones > bits {
		return store.BGPVNFConfig{}, errors.New("config.local_ip must be an IPv4 or IPv6 CIDR")
	}
	local := localIP
	if !ipv6 {
		local = localIP.To4()
	}
	peerIP := net.ParseIP(strings.TrimSpace(config.PeerIP))
	if peerIP == nil || (peerIP.To4() == nil) != ipv6 {
		return store.BGPVNFConfig{}, errors.New("config.peer_ip must use the same address family as config.local_ip")
	}
	if !ipv6 {
		peerIP = peerIP.To4()
	}
	if peerIP.Equal(local) {
		return store.BGPVNFConfig{}, errors.New("config.peer_ip cannot equal config.local_ip")
	}
	peerGateway := strings.TrimSpace(config.PeerGateway)
	if peerGateway != "" {
		gw := net.ParseIP(peerGateway)
		if gw == nil || (gw.To4() == nil) != ipv6 {
			return store.BGPVNFConfig{}, errors.New("config.peer_gateway must use the same address family as config.local_ip")
		}
		if !ipv6 {
			gw = gw.To4()
		}
		if !localNet.Contains(gw) {
			return store.BGPVNFConfig{}, errors.New("config.peer_gateway must be inside config.local_ip subnet")
		}
		peerGateway = gw.String()
	} else if !localNet.Contains(peerIP) {
		return store.BGPVNFConfig{}, errors.New("config.peer_gateway is required when peer_ip is outside the local subnet")
	}
	ttl := config.MultihopTTL
	if ttl == 0 {
		ttl = 64
	}
	if ttl > 255 {
		return store.BGPVNFConfig{}, errors.New("config.multihop_ttl must be between 1 and 255")
	}
	password := strings.TrimSpace(config.Password)
	if password != "" && transport == "memif" {
		return store.BGPVNFConfig{}, errors.New("BGP TCP MD5 password requires tap transport")
	}
	nextHop := ""
	if value := strings.TrimSpace(config.NextHop); value != "" {
		ip := net.ParseIP(value)
		if ip == nil || (ip.To4() == nil) != ipv6 {
			return store.BGPVNFConfig{}, errors.New("config.next_hop must use the same address family as config.local_ip")
		}
		if !ipv6 {
			ip = ip.To4()
		}
		nextHop = ip.String()
	}
	return store.BGPVNFConfig{
		Transport:     transport,
		LocalAS:       config.LocalAS,
		RouterID:      routerID.String(),
		PeerAS:        config.PeerAS,
		PeerIP:        peerIP.String(),
		LocalIP:       fmt.Sprintf("%s/%d", local.String(), ones),
		PeerGateway:   peerGateway,
		MultihopTTL:   ttl,
		Password:      password,
		Passive:       config.Passive,
		TargetTableID: config.TargetTableID,
		NextHop:       nextHop,
	}, nil
}

// NormalizeBGPPrefix validates and canonicalizes one advertised prefix against
// the parent's local address family.
func (s *Server) NormalizeBGPPrefix(_ context.Context, vnf store.VNF, value string) (store.BGPPrefix, error) {
	if vnf.Type != store.VNFTypeBGP {
		return store.BGPPrefix{}, errors.New("advertised prefixes are supported for BGP VNFs only")
	}
	localIP, _, err := net.ParseCIDR(vnf.Config.LocalIP)
	if err != nil {
		return store.BGPPrefix{}, fmt.Errorf("invalid BGP local_ip: %w", err)
	}
	ipv6 := localIP.To4() == nil
	ip, network, err := net.ParseCIDR(strings.TrimSpace(value))
	if err != nil || ip == nil || (ip.To4() == nil) != ipv6 {
		return store.BGPPrefix{}, errors.New("prefix must use the same address family as local_ip")
	}
	return store.BGPPrefix{Prefix: network.String()}, nil
}
