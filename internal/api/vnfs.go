package api

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/voidstarit/rookery/internal/store"
)

const (
	maxVNFNameLength = 64
	maxVNFRangeSize  = 65536
)

type vnfPayload struct {
	Name      *string           `json:"name"`
	NetworkID *int64            `json:"network_id"`
	Type      *string           `json:"type"`
	Enabled   *bool             `json:"enabled"`
	Config    *vnfConfigPayload `json:"config"`
}

type vnfUpdatePayload struct {
	Name    *string           `json:"name"`
	Enabled *bool             `json:"enabled"`
	Config  *vnfConfigPayload `json:"config"`
}

type vnfConfigPayload struct {
	Gateway          string `json:"gateway"`
	RangeStart       string `json:"range_start"`
	RangeEnd         string `json:"range_end"`
	DNS              string `json:"dns"`
	LeaseTime        string `json:"lease_time"`
	BindIP           string `json:"bind_ip"`
	OutsideInterface string `json:"outside_interface"`
	OutsideAddress   string `json:"outside_address"`
	OutsideGateway   string `json:"outside_gateway"`
	NAT44Pool        string `json:"nat44_pool"`
}

type vnfView struct {
	store.VNF
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail,omitempty"`
	LeaseCount   int    `json:"lease_count"`
}

func (p vnfPayload) toName() (string, error) {
	if p.Name == nil {
		return "", errors.New("name is required")
	}
	name := strings.TrimSpace(*p.Name)
	switch {
	case name == "":
		return "", errors.New("name is required")
	case len(name) > maxVNFNameLength:
		return "", fmt.Errorf("name must be at most %d characters", maxVNFNameLength)
	case !vmNamePattern.MatchString(name):
		return "", errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	return name, nil
}

func (p vnfPayload) toType() (string, error) {
	if p.Type == nil {
		return "", errors.New("type is required")
	}
	vnfType := strings.TrimSpace(*p.Type)
	if !store.ValidVNFType(vnfType) {
		return "", fmt.Errorf("type must be %q or %q", store.VNFTypeDHCPv4, store.VNFTypeDHCPv6)
	}
	return vnfType, nil
}

func (p vnfConfigPayload) toConfig(vnfType string) (store.VNFConfig, error) {
	gateway := strings.TrimSpace(p.Gateway)
	rangeStart := strings.TrimSpace(p.RangeStart)
	rangeEnd := strings.TrimSpace(p.RangeEnd)
	if gateway == "" {
		return store.VNFConfig{}, errors.New("config.gateway is required")
	}

	switch vnfType {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		if rangeStart == "" || rangeEnd == "" {
			return store.VNFConfig{}, errors.New("config.range_start and config.range_end are required")
		}
		leaseTime, err := normalizeLeaseTime(p.LeaseTime)
		if err != nil {
			return store.VNFConfig{}, err
		}
		var normalizedGateway, normalizedStart, normalizedEnd, dns string
		if vnfType == store.VNFTypeDHCPv4 {
			normalizedGateway, normalizedStart, normalizedEnd, dns, err = normalizeDHCPv4Config(gateway, rangeStart, rangeEnd, p.DNS)
		} else {
			normalizedGateway, normalizedStart, normalizedEnd, dns, err = normalizeDHCPv6Config(gateway, rangeStart, rangeEnd, p.DNS)
		}
		if err != nil {
			return store.VNFConfig{}, err
		}
		bindIP, err := normalizeDHCPBindIP(p.BindIP, normalizedGateway, vnfType == store.VNFTypeDHCPv6)
		if err != nil {
			return store.VNFConfig{}, err
		}
		if bindIP != "" && vnfType == store.VNFTypeDHCPv4 {
			bind, _, _ := net.ParseCIDR(bindIP)
			start := net.ParseIP(normalizedStart).To4()
			end := net.ParseIP(normalizedEnd).To4()
			if start != nil && end != nil && bytesCompareIPv4(start, bind) <= 0 && bytesCompareIPv4(end, bind) >= 0 {
				return store.VNFConfig{}, errors.New("bind_ip cannot be inside the DHCP range")
			}
		}
		return store.VNFConfig{
			Gateway:    normalizedGateway,
			RangeStart: normalizedStart,
			RangeEnd:   normalizedEnd,
			DNS:        dns,
			LeaseTime:  leaseTime,
			BindIP:     bindIP,
		}, nil
	case store.VNFTypeNAT44:
		return normalizeNAT44Config(p)
	default:
		return store.VNFConfig{}, fmt.Errorf("unsupported VNF type %q", vnfType)
	}
}

func normalizeNAT44Config(p vnfConfigPayload) (store.VNFConfig, error) {
	gateway := strings.TrimSpace(p.Gateway)
	gatewayIP, ipnet, err := net.ParseCIDR(gateway)
	if err != nil {
		return store.VNFConfig{}, fmt.Errorf("invalid NAT44 gateway CIDR: %v", err)
	}
	gateway4 := gatewayIP.To4()
	network4 := ipnet.IP.To4()
	if gateway4 == nil || network4 == nil {
		return store.VNFConfig{}, errors.New("NAT44 gateway must be an IPv4 CIDR")
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return store.VNFConfig{}, errors.New("NAT44 gateway prefix must be /30 or smaller")
	}
	broadcast := broadcastIPv4(ipnet)
	if gateway4.Equal(network4) || gateway4.Equal(broadcast) {
		return store.VNFConfig{}, errors.New("NAT44 gateway must be a usable host address")
	}
	outsideInterface := strings.TrimSpace(p.OutsideInterface)
	if outsideInterface == "" {
		return store.VNFConfig{}, errors.New("config.outside_interface is required for NAT44")
	}
	outsideAddress := strings.TrimSpace(p.OutsideAddress)
	outsideGateway := strings.TrimSpace(p.OutsideGateway)
	if outsideAddress != "" {
		ip, outsideNet, err := net.ParseCIDR(outsideAddress)
		if err != nil || ip.To4() == nil {
			return store.VNFConfig{}, errors.New("config.outside_address must be an IPv4 CIDR")
		}
		ones, bits := outsideNet.Mask.Size()
		if bits != 32 || ones == 0 {
			return store.VNFConfig{}, errors.New("config.outside_address must have a nonzero IPv4 prefix")
		}
	} else {
		outsideAddress = ""
		if outsideGateway != "" {
			return store.VNFConfig{}, errors.New("config.outside_gateway requires config.outside_address")
		}
	}
	if outsideGateway != "" {
		if ip := net.ParseIP(outsideGateway).To4(); ip == nil {
			return store.VNFConfig{}, errors.New("config.outside_gateway must be an IPv4 address")
		}
		if outsideAddress != "" {
			_, outsideNet, _ := net.ParseCIDR(outsideAddress)
			if !outsideNet.Contains(net.ParseIP(outsideGateway).To4()) {
				return store.VNFConfig{}, errors.New("config.outside_gateway must be inside the outside subnet")
			}
		}
	}
	pool := strings.TrimSpace(p.NAT44Pool)
	if pool != "" && !validNAT44Pool(pool) {
		return store.VNFConfig{}, errors.New("config.nat44_pool must be a single IPv4 address or start-end range")
	}
	return store.VNFConfig{
		Gateway:          fmt.Sprintf("%s/%d", gateway4.String(), ones),
		OutsideInterface: outsideInterface,
		OutsideAddress:   outsideAddress,
		OutsideGateway:   outsideGateway,
		NAT44Pool:        pool,
	}, nil
}

func validNAT44Pool(value string) bool {
	if strings.Contains(value, "-") {
		parts := strings.SplitN(value, "-", 2)
		start := net.ParseIP(strings.TrimSpace(parts[0])).To4()
		end := net.ParseIP(strings.TrimSpace(parts[1])).To4()
		return start != nil && end != nil && bytesCompareIPv4(start, end) <= 0
	}
	return net.ParseIP(strings.TrimSpace(value)).To4() != nil
}

func normalizeDHCPBindIP(bind, gatewayCIDR string, ipv6 bool) (string, error) {
	bind = strings.TrimSpace(bind)
	if bind == "" {
		return "", nil
	}
	ip, ipnet, err := net.ParseCIDR(bind)
	if err != nil {
		return "", fmt.Errorf("invalid bind_ip CIDR: %v", err)
	}
	if ipv6 {
		if ip.To4() != nil {
			return "", errors.New("bind_ip must be IPv6 for DHCPv6")
		}
	} else if ip.To4() == nil {
		return "", errors.New("bind_ip must be IPv4 for DHCPv4")
	}
	_, gatewayNet, err := net.ParseCIDR(gatewayCIDR)
	if err != nil {
		return "", err
	}
	gatewayIP, _, _ := net.ParseCIDR(gatewayCIDR)
	if !gatewayNet.Contains(ip) {
		return "", errors.New("bind_ip must be inside the gateway subnet")
	}
	if ip.Equal(gatewayIP) {
		return "", errors.New("bind_ip cannot equal the gateway address")
	}
	ones, bits := gatewayNet.Mask.Size()
	bindOnes, bindBits := ipnet.Mask.Size()
	if bits != bindBits || ones != bindOnes {
		return "", errors.New("bind_ip must use the gateway prefix length")
	}
	if !ipv6 {
		ip4 := ip.To4()
		if ip4.Equal(ipnet.IP.To4()) || ip4.Equal(broadcastIPv4(ipnet)) {
			return "", errors.New("bind_ip must be a usable host address")
		}
	}
	return fmt.Sprintf("%s/%d", ip.String(), bindOnes), nil
}

func normalizeLeaseTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "12h", nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return "", fmt.Errorf("invalid lease_time %q", value)
	}
	return compactDuration(duration), nil
}

func compactDuration(duration time.Duration) string {
	value := duration.String()
	value = strings.TrimSuffix(value, "0s")
	value = strings.TrimSuffix(value, "0m")
	return value
}

func normalizeDHCPv4Config(gatewayCIDR, rangeStart, rangeEnd, dns string) (string, string, string, string, error) {
	gateway, ipnet, err := net.ParseCIDR(gatewayCIDR)
	if err != nil {
		return "", "", "", "", fmt.Errorf("invalid gateway CIDR: %v", err)
	}
	gateway4 := gateway.To4()
	network4 := ipnet.IP.To4()
	if gateway4 == nil || network4 == nil {
		return "", "", "", "", errors.New("DHCPv4 gateway must be an IPv4 CIDR")
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return "", "", "", "", errors.New("DHCPv4 gateway prefix must be /30 or smaller")
	}
	broadcast := broadcastIPv4(ipnet)
	if gateway4.Equal(network4) || gateway4.Equal(broadcast) {
		return "", "", "", "", errors.New("DHCPv4 gateway must be a usable host address")
	}
	start := net.ParseIP(rangeStart).To4()
	end := net.ParseIP(rangeEnd).To4()
	if start == nil || end == nil {
		return "", "", "", "", errors.New("DHCPv4 range addresses must be valid IPv4 addresses")
	}
	for _, addr := range []net.IP{start, end} {
		if !ipnet.Contains(addr) {
			return "", "", "", "", errors.New("DHCPv4 range must be inside the gateway subnet")
		}
		if addr.Equal(network4) || addr.Equal(broadcast) {
			return "", "", "", "", errors.New("DHCPv4 range cannot include the network or broadcast address")
		}
		if addr.Equal(gateway4) {
			return "", "", "", "", errors.New("DHCPv4 range cannot include the gateway address")
		}
	}
	if bytesCompareIPv4(start, end) > 0 {
		return "", "", "", "", errors.New("DHCPv4 range start must not be after range end")
	}
	if bytesCompareIPv4(start, gateway4) <= 0 && bytesCompareIPv4(end, gateway4) >= 0 {
		return "", "", "", "", errors.New("DHCPv4 range cannot include the gateway address")
	}
	size := uint64(binaryBigEndianUint32(end)) - uint64(binaryBigEndianUint32(start)) + 1
	if size > maxVNFRangeSize {
		return "", "", "", "", fmt.Errorf("DHCPv4 range must contain at most %d addresses", maxVNFRangeSize)
	}
	normalizedDNS, err := normalizeVNFDNS(dns, false)
	if err != nil {
		return "", "", "", "", err
	}
	return fmt.Sprintf("%s/%d", gateway4.String(), ones), start.String(), end.String(), normalizedDNS, nil
}

func normalizeDHCPv6Config(gatewayCIDR, rangeStart, rangeEnd, dns string) (string, string, string, string, error) {
	prefix, err := netip.ParsePrefix(gatewayCIDR)
	if err != nil || !prefix.Addr().Is6() {
		return "", "", "", "", errors.New("DHCPv6 gateway must be an IPv6 CIDR")
	}
	gateway := prefix.Addr()
	if gateway.IsUnspecified() {
		return "", "", "", "", errors.New("DHCPv6 gateway must be a usable address")
	}
	start, err := netip.ParseAddr(rangeStart)
	if err != nil || !start.Is6() {
		return "", "", "", "", errors.New("DHCPv6 range start must be a valid IPv6 address")
	}
	end, err := netip.ParseAddr(rangeEnd)
	if err != nil || !end.Is6() {
		return "", "", "", "", errors.New("DHCPv6 range end must be a valid IPv6 address")
	}
	if !prefix.Contains(start) || !prefix.Contains(end) {
		return "", "", "", "", errors.New("DHCPv6 range must be inside the gateway prefix")
	}
	if start.Compare(end) > 0 {
		return "", "", "", "", errors.New("DHCPv6 range start must not be after range end")
	}
	if start.Compare(gateway) <= 0 && end.Compare(gateway) >= 0 {
		return "", "", "", "", errors.New("DHCPv6 range cannot include the gateway address")
	}
	anycast := prefix.Masked().Addr()
	if start.Compare(anycast) <= 0 && end.Compare(anycast) >= 0 {
		return "", "", "", "", errors.New("DHCPv6 range cannot include the subnet-router anycast address")
	}
	startInt := new(big.Int).SetBytes(start.AsSlice())
	endInt := new(big.Int).SetBytes(end.AsSlice())
	size := new(big.Int).Sub(endInt, startInt)
	size.Add(size, big.NewInt(1))
	if size.Cmp(big.NewInt(maxVNFRangeSize)) > 0 {
		return "", "", "", "", fmt.Errorf("DHCPv6 range must contain at most %d addresses", maxVNFRangeSize)
	}
	normalizedDNS, err := normalizeVNFDNS(dns, true)
	if err != nil {
		return "", "", "", "", err
	}
	return prefix.String(), start.String(), end.String(), normalizedDNS, nil
}

func normalizeVNFDNS(value string, ipv6 bool) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return "", fmt.Errorf("invalid DNS address %q", part)
		}
		if ipv6 {
			if ip.To4() != nil {
				return "", fmt.Errorf("DNS address %q is not IPv6", part)
			}
			out = append(out, ip.To16().String())
		} else {
			if ip.To4() == nil {
				return "", fmt.Errorf("DNS address %q is not IPv4", part)
			}
			out = append(out, ip.To4().String())
		}
	}
	return strings.Join(out, ","), nil
}

func binaryBigEndianUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
}

func bytesCompareIPv4(a, b net.IP) int {
	av := binaryBigEndianUint32(a)
	bv := binaryBigEndianUint32(b)
	switch {
	case av < bv:
		return -1
	case av > bv:
		return 1
	default:
		return 0
	}
}

func parseVNFID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid VNF id")
	}
	return id, nil
}

func (s *Server) handleListVNFs(w http.ResponseWriter, r *http.Request) {
	var networkID *int64
	if raw := strings.TrimSpace(r.URL.Query().Get("network_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			writeError(w, http.StatusBadRequest, "invalid network_id")
			return
		}
		networkID = &id
	}
	vnfs, err := s.store.ListVNFs(r.Context(), networkID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	views := make([]vnfView, 0, len(vnfs))
	for _, vnf := range vnfs {
		views = append(views, s.viewVNF(r.Context(), vnf))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleGetVNF(w http.ResponseWriter, r *http.Request) {
	id, err := parseVNFID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vnf, err := s.store.GetVNF(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		writeError(w, http.StatusNotFound, "vnf not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, s.viewVNF(r.Context(), vnf))
	}
}

func (s *Server) handleCreateVNF(w http.ResponseWriter, r *http.Request) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	var payload vnfPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := payload.toName()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vnfType, err := payload.toType()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if payload.NetworkID == nil || *payload.NetworkID < 1 {
		writeError(w, http.StatusBadRequest, "network_id is required")
		return
	}
	if payload.Config == nil {
		writeError(w, http.StatusBadRequest, "config is required")
		return
	}
	config, err := payload.Config.toConfig(vnfType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	network, err := s.store.GetNetwork(r.Context(), *payload.NetworkID)
	switch {
	case errors.Is(err, store.ErrNetworkNotFound):
		writeError(w, http.StatusBadRequest, "network not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	existingVNFs, err := s.store.ListVNFsByNetwork(r.Context(), network.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, existing := range existingVNFs {
		if existing.Type == vnfType {
			writeError(w, http.StatusConflict, fmt.Sprintf("network already has a %s VNF", vnfType))
			return
		}
	}

	if err := applyNATGatewayToDHCPConfig(network, vnfType, &config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	enabled := payload.Enabled != nil && *payload.Enabled
	if enabled {
		if blocked, message := s.vppBlocked(); blocked {
			writeError(w, http.StatusServiceUnavailable, message)
			return
		}
		if s.vnfs == nil || s.networks == nil {
			writeError(w, http.StatusServiceUnavailable, "VNF runtime is not configured")
			return
		}
		if vnfType == store.VNFTypeNAT44 && s.nat == nil {
			writeError(w, http.StatusServiceUnavailable, "NAT44 runtime is not configured")
			return
		}
	}

	created, err := s.store.CreateVNF(r.Context(), store.VNF{
		Name:      name,
		NetworkID: network.ID,
		Type:      vnfType,
		Enabled:   false,
		Config:    config,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrVNFNameTaken):
			writeError(w, http.StatusConflict, "a VNF with that name already exists")
		default:
			s.internalError(w, r, err)
		}
		return
	}

	if enabled {
		var activated store.VNF
		var err error
		if vnfType == store.VNFTypeNAT44 {
			activated, err = s.activateNAT44(r.Context(), created)
		} else {
			activated, err = s.activateVNF(r.Context(), created)
		}
		if err != nil {
			_ = s.store.DeleteVNF(r.Context(), created.ID)
			s.logger.Error("start VNF failed", "vnf", created.Name, "error", err)
			writeError(w, http.StatusInternalServerError, "could not start VNF: "+err.Error())
			return
		}
		created = activated
	}

	w.Header().Set("Location", fmt.Sprintf("/api/vnfs/%d", created.ID))
	writeJSON(w, http.StatusCreated, s.viewVNF(r.Context(), created))
}

func (s *Server) handleUpdateVNF(w http.ResponseWriter, r *http.Request) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	id, err := parseVNFID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var payload vnfUpdatePayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.store.GetVNF(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		writeError(w, http.StatusNotFound, "vnf not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	name := existing.Name
	if payload.Name != nil {
		name, err = vnfUpdateName(payload.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	config := existing.Config
	if payload.Config != nil {
		config, err = payload.Config.toConfig(existing.Type)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if existing.Type == store.VNFTypeDHCPv4 {
		if network, err := s.store.GetNetwork(r.Context(), existing.NetworkID); err == nil {
			if err := applyNATGatewayToDHCPConfig(network, existing.Type, &config); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	enabled := existing.Enabled
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	if enabled {
		if blocked, message := s.vppBlocked(); blocked {
			writeError(w, http.StatusServiceUnavailable, message)
			return
		}
		if s.vnfs == nil || s.networks == nil {
			writeError(w, http.StatusServiceUnavailable, "VNF runtime is not configured")
			return
		}
		if existing.Type == store.VNFTypeNAT44 && s.nat == nil {
			writeError(w, http.StatusServiceUnavailable, "NAT44 runtime is not configured")
			return
		}
	}

	updated := existing
	updated.Name = name
	updated.Config = config
	updated.Enabled = enabled

	if existing.Type == store.VNFTypeNAT44 {
		if err := s.stopNAT44(r.Context(), existing); err != nil {
			s.logger.Error("stop NAT44 for update failed", "vnf", existing.Name, "error", err)
			writeError(w, http.StatusInternalServerError, "could not stop NAT44: "+err.Error())
			return
		}
		updated.Enabled = false
		updated.TapSwIfIndex = 0
		updated.TapHostName = ""
		if enabled {
			activated, err := s.activateNAT44(r.Context(), updated)
			if err != nil {
				if _, saveErr := s.store.UpdateVNF(r.Context(), id, updated); saveErr != nil {
					s.logger.Error("save disabled NAT44 after start failure failed", "vnf", id, "error", saveErr)
				}
				s.logger.Error("start NAT44 failed", "vnf", name, "error", err)
				writeError(w, http.StatusInternalServerError, "could not start NAT44: "+err.Error())
				return
			}
			updated = activated
		} else {
			saved, err := s.store.UpdateVNF(r.Context(), id, updated)
			if err != nil {
				writeUpdateVNFError(w, r, s, err)
				return
			}
			updated = saved
		}
		writeJSON(w, http.StatusOK, s.viewVNF(r.Context(), updated))
		return
	}

	if existing.Enabled || existing.TapSwIfIndex > 0 {
		if err := s.stopVNF(r.Context(), existing, false); err != nil {
			s.logger.Error("stop VNF for update failed", "vnf", existing.Name, "error", err)
			writeError(w, http.StatusInternalServerError, "could not stop VNF: "+err.Error())
			return
		}
		updated.TapSwIfIndex = 0
		updated.TapHostName = ""
	}

	if enabled {
		activated, err := s.activateVNF(r.Context(), updated)
		if err != nil {
			updated.Enabled = false
			updated.TapSwIfIndex = 0
			updated.TapHostName = ""
			if _, saveErr := s.store.UpdateVNF(r.Context(), id, updated); saveErr != nil {
				s.logger.Error("save disabled VNF after start failure failed", "vnf", id, "error", saveErr)
			}
			s.logger.Error("start VNF failed", "vnf", name, "error", err)
			writeError(w, http.StatusInternalServerError, "could not start VNF: "+err.Error())
			return
		}
		updated = activated
	} else {
		saved, err := s.store.UpdateVNF(r.Context(), id, updated)
		if err != nil {
			writeUpdateVNFError(w, r, s, err)
			return
		}
		updated = saved
	}

	writeJSON(w, http.StatusOK, s.viewVNF(r.Context(), updated))
}

func writeUpdateVNFError(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	switch {
	case errors.Is(err, store.ErrVNFNameTaken):
		writeError(w, http.StatusConflict, "a VNF with that name already exists")
	case errors.Is(err, store.ErrVNFNotFound):
		writeError(w, http.StatusNotFound, "vnf not found")
	default:
		s.internalError(w, r, err)
	}
}

func vnfUpdateName(value *string) (string, error) {
	if value == nil {
		return "", errors.New("name is required")
	}
	name := strings.TrimSpace(*value)
	switch {
	case name == "":
		return "", errors.New("name is required")
	case len(name) > maxVNFNameLength:
		return "", fmt.Errorf("name must be at most %d characters", maxVNFNameLength)
	case !vmNamePattern.MatchString(name):
		return "", errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	return name, nil
}

func (s *Server) handleDeleteVNF(w http.ResponseWriter, r *http.Request) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	id, err := parseVNFID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vnf, err := s.store.GetVNF(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		writeError(w, http.StatusNotFound, "vnf not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	var stopErr error
	if vnf.Type == store.VNFTypeNAT44 {
		stopErr = s.stopNAT44(r.Context(), vnf)
	} else {
		stopErr = s.stopVNF(r.Context(), vnf, true)
	}
	if stopErr != nil {
		s.logger.Error("stop VNF before delete failed", "vnf", vnf.Name, "error", stopErr)
		writeError(w, http.StatusInternalServerError, "could not stop VNF: "+stopErr.Error())
		return
	}
	err = s.store.DeleteVNF(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		writeError(w, http.StatusNotFound, "vnf not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		if vnf.Type == store.VNFTypeNAT44 {
			if err := s.cleanupNetworkBVIAfterNATDelete(r.Context(), vnf.NetworkID); err != nil {
				s.logger.Error("cleanup network BVI after NAT44 delete failed", "vnf", vnf.Name, "network", vnf.NetworkName, "error", err)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleRestartVNF(w http.ResponseWriter, r *http.Request) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	id, err := parseVNFID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vnf, err := s.store.GetVNF(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		writeError(w, http.StatusNotFound, "vnf not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if s.vnfs == nil || s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VNF runtime is not configured")
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}
	if vnf.Type == store.VNFTypeNAT44 && s.nat == nil {
		writeError(w, http.StatusServiceUnavailable, "NAT44 runtime is not configured")
		return
	}
	var stopErr error
	if vnf.Type == store.VNFTypeNAT44 {
		stopErr = s.stopNAT44(r.Context(), vnf)
	} else {
		stopErr = s.stopVNF(r.Context(), vnf, false)
	}
	if stopErr != nil {
		s.logger.Error("stop VNF for restart failed", "vnf", vnf.Name, "error", stopErr)
		writeError(w, http.StatusInternalServerError, "could not stop VNF: "+stopErr.Error())
		return
	}
	vnf.Enabled = true
	vnf.TapSwIfIndex = 0
	vnf.TapHostName = ""
	var activated store.VNF
	if vnf.Type == store.VNFTypeNAT44 {
		activated, err = s.activateNAT44(r.Context(), vnf)
	} else {
		activated, err = s.activateVNF(r.Context(), vnf)
	}
	if err != nil {
		vnf.Enabled = false
		if _, saveErr := s.store.UpdateVNF(r.Context(), id, vnf); saveErr != nil {
			s.logger.Error("save disabled VNF after restart failure failed", "vnf", id, "error", saveErr)
		}
		s.logger.Error("restart VNF failed", "vnf", vnf.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "could not restart VNF: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.viewVNF(r.Context(), activated))
}

func (s *Server) handleListVNFLeases(w http.ResponseWriter, r *http.Request) {
	id, err := parseVNFID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vnf, err := s.store.GetVNF(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		writeError(w, http.StatusNotFound, "vnf not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if s.vnfs == nil {
		writeJSON(w, http.StatusOK, []store.VNFLease{})
		return
	}
	leases, err := s.vnfs.Leases(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	filtered := make([]store.VNFLease, 0, len(leases))
	for _, lease := range leases {
		if leaseBelongsToVNF(vnf, lease.Address) {
			filtered = append(filtered, lease)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vnf_id": vnf.ID,
		"type":   vnf.Type,
		"leases": filtered,
	})
}

func leaseBelongsToVNF(vnf store.VNF, address string) bool {
	switch vnf.Type {
	case store.VNFTypeDHCPv4:
		_, ipnet, err := net.ParseCIDR(vnf.Config.Gateway)
		if err != nil {
			return false
		}
		ip := net.ParseIP(address).To4()
		start := net.ParseIP(vnf.Config.RangeStart).To4()
		end := net.ParseIP(vnf.Config.RangeEnd).To4()
		if ip == nil || start == nil || end == nil || !ipnet.Contains(ip) {
			return false
		}
		value := binaryBigEndianUint32(ip)
		return value >= binaryBigEndianUint32(start) && value <= binaryBigEndianUint32(end)
	case store.VNFTypeDHCPv6:
		prefix, err := netip.ParsePrefix(vnf.Config.Gateway)
		if err != nil {
			return false
		}
		addr, err := netip.ParseAddr(address)
		if err != nil || !addr.Is6() || !prefix.Contains(addr) {
			return false
		}
		start, err := netip.ParseAddr(vnf.Config.RangeStart)
		if err != nil {
			return false
		}
		end, err := netip.ParseAddr(vnf.Config.RangeEnd)
		if err != nil {
			return false
		}
		return addr.Compare(start) >= 0 && addr.Compare(end) <= 0
	default:
		return false
	}
}

func (s *Server) viewVNF(ctx context.Context, vnf store.VNF) vnfView {
	view := vnfView{VNF: vnf, Status: "disabled"}
	if vnf.Enabled {
		if s.vnfRunning(vnf) {
			view.Status = "running"
		} else {
			view.Status = "degraded"
			view.StatusDetail = "runtime is not running; use Restart (recreate the network first if VPP was restarted)"
		}
	}
	if s.vnfs != nil {
		if leases, err := s.vnfs.Leases(ctx, vnf.ID); err == nil {
			for _, lease := range leases {
				if lease.Active && leaseBelongsToVNF(vnf, lease.Address) {
					view.LeaseCount++
				}
			}
		}
	}
	return view
}

func (s *Server) vnfRunning(vnf store.VNF) bool {
	if vnf.Type == store.VNFTypeNAT44 {
		s.natMu.Lock()
		defer s.natMu.Unlock()
		return s.natRunning[vnf.ID]
	}
	return s.vnfs != nil && s.vnfs.Running(vnf.ID)
}

func (s *Server) setNAT44Running(vnfID int64, running bool) {
	s.natMu.Lock()
	defer s.natMu.Unlock()
	if s.natRunning == nil {
		s.natRunning = make(map[int64]bool)
	}
	if running {
		s.natRunning[vnfID] = true
	} else {
		delete(s.natRunning, vnfID)
	}
}

func (s *Server) handleListVNFSessions(w http.ResponseWriter, r *http.Request) {
	id, err := parseVNFID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vnf, err := s.store.GetVNF(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		writeError(w, http.StatusNotFound, "vnf not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if vnf.Type != store.VNFTypeNAT44 {
		writeError(w, http.StatusBadRequest, "sessions are available for NAT44 VNFs only")
		return
	}
	if s.nat == nil {
		writeJSON(w, http.StatusOK, map[string]any{"vnf_id": vnf.ID, "type": vnf.Type, "sessions": []string{}})
		return
	}
	sessions, err := s.nat.NAT44Sessions(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if sessions == nil {
		sessions = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vnf_id":   vnf.ID,
		"type":     vnf.Type,
		"sessions": sessions,
	})
}

func applyNATGatewayToDHCPConfig(network store.Network, vnfType string, config *store.VNFConfig) error {
	if vnfType != store.VNFTypeDHCPv4 || network.IPv4Gateway == "" {
		return nil
	}
	natIP, natSubnet, err := net.ParseCIDR(network.IPv4Gateway)
	if err != nil {
		return fmt.Errorf("parse network IPv4 gateway: %w", err)
	}
	if natIP.To4() == nil {
		return errors.New("network IPv4 gateway is invalid")
	}
	start := net.ParseIP(config.RangeStart).To4()
	end := net.ParseIP(config.RangeEnd).To4()
	if start == nil || end == nil || !natSubnet.Contains(start) || !natSubnet.Contains(end) {
		return fmt.Errorf("DHCPv4 range must be inside the network NAT subnet %s", natSubnet)
	}
	bindCIDR, err := nextIPv4CIDR(network.IPv4Gateway)
	if err != nil {
		return err
	}
	bindIP, _, _ := net.ParseCIDR(bindCIDR)
	if bytesCompareIPv4(start, bindIP) <= 0 && bytesCompareIPv4(end, bindIP) >= 0 {
		return fmt.Errorf("DHCPv4 range cannot include the NAT service address %s", bindIP)
	}
	config.Gateway = network.IPv4Gateway
	config.BindIP = bindCIDR
	return nil
}

func (s *Server) activateVNF(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	if s.vnfs == nil || s.networks == nil {
		return vnf, errors.New("VNF runtime is not configured")
	}
	if blocked, message := s.vppBlocked(); blocked {
		return vnf, errors.New(message)
	}
	network, err := s.store.GetNetwork(ctx, vnf.NetworkID)
	if err != nil {
		return vnf, fmt.Errorf("get VNF network: %w", err)
	}
	if err := s.cleanupLegacyNetworkVNF(ctx, network); err != nil {
		return vnf, err
	}

	hostIf := fmt.Sprintf("vnf%d", vnf.ID)
	tapSwIfIndex, err := s.networks.CreateTap(ctx, hostIf, vnfTapCIDR(vnf), network.BridgeDomainID)
	if err != nil {
		return vnf, fmt.Errorf("create VNF tap: %w", err)
	}
	if err := s.vnfs.Start(ctx, vnf, hostIf); err != nil {
		_ = s.networks.DeleteTap(ctx, tapSwIfIndex)
		return vnf, err
	}

	vnf.Enabled = true
	vnf.TapSwIfIndex = tapSwIfIndex
	vnf.TapHostName = hostIf
	updated, err := s.store.UpdateVNF(ctx, vnf.ID, vnf)
	if err != nil {
		_ = s.vnfs.Stop(ctx, vnf.ID, false)
		_ = s.networks.DeleteTap(ctx, tapSwIfIndex)
		return vnf, err
	}
	return updated, nil
}

func (s *Server) stopVNF(ctx context.Context, vnf store.VNF, removeLeases bool) error {
	var errs []error
	if s.vnfs != nil {
		if err := s.vnfs.Stop(ctx, vnf.ID, removeLeases); err != nil {
			errs = append(errs, fmt.Errorf("stop VNF runtime: %w", err))
		}
	}
	if s.networks != nil {
		if err := s.deleteTap(ctx, vnf.TapSwIfIndex, vnf.TapHostName); err != nil {
			errs = append(errs, fmt.Errorf("delete VNF tap: %w", err))
		}
	}
	return errors.Join(errs...)
}

type tapDeleterByName interface {
	DeleteTapByName(ctx context.Context, hostName string) error
}

func (s *Server) deleteTap(ctx context.Context, swIfIndex uint32, hostName string) error {
	if swIfIndex == 0 && strings.TrimSpace(hostName) == "" {
		return nil
	}
	if hostName != "" {
		if deleter, ok := s.networks.(tapDeleterByName); ok {
			return deleter.DeleteTapByName(ctx, hostName)
		}
	}
	if swIfIndex == 0 {
		return nil
	}
	return s.networks.DeleteTap(ctx, swIfIndex)
}

func (s *Server) cleanupLegacyNetworkVNF(ctx context.Context, network store.Network) error {
	if network.DHCPTapSwIfIndex > 0 || network.DHCPTapHostName != "" {
		if err := s.deleteTap(ctx, network.DHCPTapSwIfIndex, network.DHCPTapHostName); err != nil {
			return fmt.Errorf("delete legacy network DHCP tap: %w", err)
		}
	}
	if network.DHCPEnabled || network.DHCPTapSwIfIndex > 0 {
		if _, err := s.store.SetNetworkDHCP(ctx, network.ID, store.Network{}); err != nil {
			return fmt.Errorf("clear legacy network DHCP: %w", err)
		}
	}
	if s.vnfs != nil {
		s.vnfs.CleanupLegacyNetwork(network.ID)
	}
	return nil
}
