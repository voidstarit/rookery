package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// VNFTypeDHCPv4 is a DHCPv4 server function.
	VNFTypeDHCPv4 = "dhcpv4"
	// VNFTypeDHCPv6 is a DHCPv6 server function.
	VNFTypeDHCPv6 = "dhcpv6"
	// VNFTypeNAT44 is a VPP NAT44 function.
	VNFTypeNAT44 = "nat44"
	// VNFTypeBGP is an embedded IPv4 BGP speaker.
	VNFTypeBGP = "bgp"
	// VNFTypeDNS is a local DNS server with optional upstream forwarding.
	VNFTypeDNS = "dns"
)

// ValidVNFType reports whether type is a supported network function.
func ValidVNFType(vnfType string) bool {
	return vnfType == VNFTypeDHCPv4 || vnfType == VNFTypeDHCPv6 || vnfType == VNFTypeNAT44 || vnfType == VNFTypeBGP || vnfType == VNFTypeDNS
}

// VNFConfig is the shared runtime hydration shape for a network function. It is
// read-only: typed store write methods are the only way to persist a VNF, and
// the configuration is never stored in this shape.
type VNFConfig struct {
	Gateway    string `json:"gateway"`
	RangeStart string `json:"range_start"`
	RangeEnd   string `json:"range_end"`
	DNS        string `json:"dns,omitempty"`
	LeaseTime  string `json:"lease_time,omitempty"`

	// BindIP is the address assigned to a DHCP VNF's Linux tap. It is normally
	// empty (the gateway is used). On a NAT network it is the .2 address so the
	// BVI can own the .1 gateway.
	BindIP string `json:"bind_ip,omitempty"`

	// StaticAssignments reserve IPv4 addresses for specific MAC addresses.
	StaticAssignments []DHCPStaticAssignment `json:"static_assignments,omitempty"`

	// NAT44 configuration.
	OutsideInterface string `json:"outside_interface,omitempty"`
	OutsideAddress   string `json:"outside_address,omitempty"`
	OutsideGateway   string `json:"outside_gateway,omitempty"`
	NAT44Pool        string `json:"nat44_pool,omitempty"`

	// DNATRules expose services on the NAT44 network to clients on the
	// outside interface. The external address is the outside interface's
	// address, so rules keep working when the address is DHCP-assigned.
	DNATRules []DNATRule `json:"dnat_rules,omitempty"`

	// BGP configuration. Address family follows LocalIP (IPv4 or IPv6).
	Transport         string   `json:"transport,omitempty"`
	LocalAS           uint32   `json:"local_as,omitempty"`
	RouterID          string   `json:"router_id,omitempty"`
	PeerAS            uint32   `json:"peer_as,omitempty"`
	PeerIP            string   `json:"peer_ip,omitempty"`
	LocalIP           string   `json:"local_ip,omitempty"`
	PeerGateway       string   `json:"peer_gateway,omitempty"`
	MultihopTTL       uint32   `json:"multihop_ttl,omitempty"`
	Password          string   `json:"password,omitempty"`
	Passive           bool     `json:"passive,omitempty"`
	TargetTableID     uint32   `json:"target_table_id,omitempty"`
	AdvertisePrefixes []string `json:"advertise_prefixes,omitempty"`
	// NextHop overrides the NEXT_HOP announced with advertised prefixes.
	// Empty means LocalIP is advertised.
	NextHop string `json:"next_hop,omitempty"`

	// DNS configuration. Records are served authoritatively; names without a
	// local record are forwarded when Forward is enabled.
	DNSRecords    []DNSRecord `json:"dns_records,omitempty"`
	DNSForward    bool        `json:"dns_forward,omitempty"`
	DNSForwarders []string    `json:"dns_forwarders,omitempty"`

	// DHCP DNS integration. A DHCPv4/DHCPv6 VNF can register dynamic A/AAAA
	// and PTR records for its leases in another DNS VNF on the same network.
	// DNSDomain is the suffix used when the client does not send a FQDN;
	// DNSTTL defaults to 300 seconds.
	DNSVNFID  int64  `json:"dns_vnf_id,omitempty"`
	DNSDomain string `json:"dns_domain,omitempty"`
	DNSTTL    uint32 `json:"dns_ttl,omitempty"`

	// DHCPOptions are extra DHCPv4/DHCPv6 options advertised by a DHCP VNF.
	// Code is the DHCP option code; Format controls how Value is encoded.
	DHCPOptions []DHCPOption `json:"dhcp_options,omitempty"`
}

// DHCPOption is one extra option advertised by a DHCP VNF.
type DHCPOption struct {
	ID     int64  `json:"id,omitempty"`
	VNFID  int64  `json:"vnf_id,omitempty"`
	Code   uint16 `json:"code"`
	Format string `json:"format"`
	Value  string `json:"value"`
}

// DNSRecord is one authoritative record served by a DNS VNF.
type DNSRecord struct {
	ID    int64  `json:"id,omitempty"`
	VNFID int64  `json:"vnf_id,omitempty"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   uint32 `json:"ttl,omitempty"`
}

// DNSForwarder is one upstream resolver used by a DNS VNF. Position orders the
// forwarders.
type DNSForwarder struct {
	ID       int64  `json:"id,omitempty"`
	VNFID    int64  `json:"vnf_id,omitempty"`
	Address  string `json:"address"`
	Position int32  `json:"position"`
}

// DHCPStaticAssignment reserves one IPv4 address for a MAC address.
type DHCPStaticAssignment struct {
	ID    int64  `json:"id,omitempty"`
	VNFID int64  `json:"vnf_id,omitempty"`
	MAC   string `json:"mac"`
	IP    string `json:"ip"`
}

// DNATRule forwards one external TCP or UDP port to an address inside a NAT44
// network.
type DNATRule struct {
	ID           int64  `json:"id,omitempty"`
	VNFID        int64  `json:"vnf_id,omitempty"`
	Protocol     string `json:"protocol"`
	ExternalPort int    `json:"external_port"`
	LocalIP      string `json:"local_ip"`
	LocalPort    int    `json:"local_port"`
}

// BGPPrefix is one prefix advertised by a BGP VNF.
type BGPPrefix struct {
	ID     int64  `json:"id,omitempty"`
	VNFID  int64  `json:"vnf_id,omitempty"`
	Prefix string `json:"prefix"`
}

// VNF is one network function identity plus its hydrated runtime config. The
// identity fields are persisted in vnfs; Config is assembled from the typed
// parent and child tables and is only used by the runtime and validation code.
type VNF struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	NetworkID      int64     `json:"network_id"`
	NetworkName    string    `json:"network_name"`
	Type           string    `json:"type"`
	Enabled        bool      `json:"enabled"`
	Config         VNFConfig `json:"config"`
	TapSwIfIndex   uint32    `json:"tap_sw_if_index,omitempty"`
	TapHostName    string    `json:"tap_host_name,omitempty"`
	MemifSwIfIndex uint32    `json:"memif_sw_if_index,omitempty"`
	MemifSocket    string    `json:"memif_socket,omitempty"`
	MemifHostName  string    `json:"memif_host_name,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// VNFLease is one address handed out by a DHCP network function.
type VNFLease struct {
	ClientID  string    `json:"client_id"`
	Address   string    `json:"address"`
	Hostname  string    `json:"hostname,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	Active    bool      `json:"active"`
}

// DHCPVNFConfig is the scalar, typed configuration of a DHCP VNF.
type DHCPVNFConfig struct {
	Gateway    string `json:"gateway"`
	RangeStart string `json:"range_start"`
	RangeEnd   string `json:"range_end"`
	LeaseTime  string `json:"lease_time"`
	DNS        string `json:"dns"`
	BindIP     string `json:"bind_ip"`
	Transport  string `json:"transport"`
	DNSVNFID   int64  `json:"dns_vnf_id"`
	DNSDomain  string `json:"dns_domain"`
	DNSTTL     uint32 `json:"dns_ttl"`
}

// DNSVNFConfig is the scalar, typed configuration of a DNS VNF.
type DNSVNFConfig struct {
	Forward bool `json:"forward"`
}

// NAT44VNFConfig is the scalar, typed configuration of a NAT44 VNF.
type NAT44VNFConfig struct {
	Gateway          string `json:"gateway"`
	OutsideInterface string `json:"outside_interface"`
	OutsideAddress   string `json:"outside_address"`
	OutsideGateway   string `json:"outside_gateway"`
	NAT44Pool        string `json:"nat44_pool"`
}

// BGPVNFConfig is the scalar, typed configuration of a BGP VNF. Password is
// write-only: reads never hydrate it.
type BGPVNFConfig struct {
	Transport     string `json:"transport"`
	LocalAS       uint32 `json:"local_as"`
	RouterID      string `json:"router_id"`
	PeerAS        uint32 `json:"peer_as"`
	PeerIP        string `json:"peer_ip"`
	LocalIP       string `json:"local_ip"`
	PeerGateway   string `json:"peer_gateway"`
	MultihopTTL   uint32 `json:"multihop_ttl"`
	Password      string `json:"password"`
	Passive       bool   `json:"passive"`
	TargetTableID uint32 `json:"target_table_id"`
	NextHop       string `json:"next_hop"`
}

// DHCPVNF is one DHCPv4 or DHCPv6 VNF read model: identity, scalar config, and
// child counts. Child rows are loaded separately.
type DHCPVNF struct {
	ID                    int64         `json:"id"`
	Name                  string        `json:"name"`
	NetworkID             int64         `json:"network_id"`
	NetworkName           string        `json:"network_name"`
	Family                string        `json:"family"`
	Enabled               bool          `json:"enabled"`
	Config                DHCPVNFConfig `json:"config"`
	StaticAssignmentCount int           `json:"static_assignment_count"`
	OptionCount           int           `json:"option_count"`
	TapSwIfIndex          uint32        `json:"tap_sw_if_index,omitempty"`
	TapHostName           string        `json:"tap_host_name,omitempty"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
}

// DNSVNF is one DNS VNF read model with child counts.
type DNSVNF struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	NetworkID      int64        `json:"network_id"`
	NetworkName    string       `json:"network_name"`
	Enabled        bool         `json:"enabled"`
	Config         DNSVNFConfig `json:"config"`
	RecordCount    int          `json:"record_count"`
	ForwarderCount int          `json:"forwarder_count"`
	TapSwIfIndex   uint32       `json:"tap_sw_if_index,omitempty"`
	TapHostName    string       `json:"tap_host_name,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// NAT44VNF is one NAT44 VNF read model with child counts.
type NAT44VNF struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	NetworkID     int64          `json:"network_id"`
	NetworkName   string         `json:"network_name"`
	Enabled       bool           `json:"enabled"`
	Config        NAT44VNFConfig `json:"config"`
	DNATRuleCount int            `json:"dnat_rule_count"`
	TapSwIfIndex  uint32         `json:"tap_sw_if_index,omitempty"`
	TapHostName   string         `json:"tap_host_name,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// BGPVNF is one BGP VNF read model with child counts. It never carries the TCP
// MD5 password.
type BGPVNF struct {
	ID                    int64        `json:"id"`
	Name                  string       `json:"name"`
	NetworkID             int64        `json:"network_id"`
	NetworkName           string       `json:"network_name"`
	Enabled               bool         `json:"enabled"`
	Config                BGPVNFConfig `json:"config"`
	AdvertisedPrefixCount int          `json:"advertised_prefix_count"`
	TapSwIfIndex          uint32       `json:"tap_sw_if_index,omitempty"`
	TapHostName           string       `json:"tap_host_name,omitempty"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
}

// NewDHCPVNF describes a DHCP VNF to insert.
type NewDHCPVNF struct {
	Name      string
	NetworkID int64
	Family    string
	Enabled   bool
	Config    DHCPVNFConfig
}

// NewDNSVNF describes a DNS VNF to insert.
type NewDNSVNF struct {
	Name      string
	NetworkID int64
	Enabled   bool
	Config    DNSVNFConfig
}

// NewNAT44VNF describes a NAT44 VNF to insert.
type NewNAT44VNF struct {
	Name      string
	NetworkID int64
	Enabled   bool
	Config    NAT44VNFConfig
}

// NewBGPVNF describes a BGP VNF to insert.
type NewBGPVNF struct {
	Name      string
	NetworkID int64
	Enabled   bool
	Config    BGPVNFConfig
}

var (
	// ErrVNFNotFound is returned when a network function does not exist.
	ErrVNFNotFound = errors.New("vnf not found")
	// ErrVNFNameTaken is returned when a network function name is already in use.
	ErrVNFNameTaken = errors.New("vnf name already taken")
	// ErrVNFTypeTaken is returned when a network already has a VNF of that type.
	ErrVNFTypeTaken = errors.New("network already has a vnf of that type")
	// ErrVNFTypeMismatch is returned when an ID belongs to a VNF of another type.
	ErrVNFTypeMismatch = errors.New("vnf type mismatch")
	// ErrVNFChildNotFound is returned when a child row does not exist.
	ErrVNFChildNotFound = errors.New("vnf child not found")
	// ErrVNFChildTaken is returned when a child row violates a unique key.
	ErrVNFChildTaken = errors.New("vnf child already exists")
)

const vnfIdentitySelect = `
	SELECT f.id, f.name, f.network_id, n.name, f.type, f.enabled,
	       f.tap_sw_if_index, f.tap_host_name, f.memif_sw_if_index,
	       f.memif_socket, f.memif_host_name, f.created_at, f.updated_at
	FROM vnfs AS f
	JOIN networks AS n ON n.id = f.network_id`

const vnfIdentityUpdate = `
	UPDATE vnfs
	SET name = ?, enabled = ?, tap_sw_if_index = ?, tap_host_name = ?,
	    memif_sw_if_index = ?, memif_socket = ?, memif_host_name = ?,
	    updated_at = ?
	WHERE id = ?`

// scanVNFIdentity decodes one vnfs identity row joined with its network name.
func scanVNFIdentity(s scanner) (VNF, error) {
	var (
		vnf       VNF
		enabled   int64
		tapSwIf   int64
		memifSwIf int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&vnf.ID, &vnf.Name, &vnf.NetworkID, &vnf.NetworkName, &vnf.Type,
		&enabled, &tapSwIf, &vnf.TapHostName, &memifSwIf, &vnf.MemifSocket,
		&vnf.MemifHostName, &createdAt, &updatedAt,
	); err != nil {
		return VNF{}, err
	}
	vnf.Enabled = enabled != 0
	vnf.TapSwIfIndex = uint32(tapSwIf)
	vnf.MemifSwIfIndex = uint32(memifSwIf)
	vnf.CreatedAt = time.UnixMilli(createdAt).UTC()
	vnf.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return vnf, nil
}

// identityVNF returns one identity row without hydration.
func (s *Store) identityVNF(ctx context.Context, id int64) (VNF, error) {
	vnf, err := scanVNFIdentity(s.db.QueryRowContext(ctx, vnfIdentitySelect+` WHERE f.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return VNF{}, ErrVNFNotFound
	}
	if err != nil {
		return VNF{}, fmt.Errorf("query vnf: %w", err)
	}
	return vnf, nil
}

// GetVNF returns one network function by ID with its runtime config hydrated.
func (s *Store) GetVNF(ctx context.Context, id int64) (VNF, error) {
	vnf, err := s.identityVNF(ctx, id)
	if err != nil {
		return VNF{}, err
	}
	if err := s.hydrateVNF(ctx, &vnf); err != nil {
		return VNF{}, err
	}
	return vnf, nil
}

// ListVNFs returns network functions ordered by name, optionally filtered by
// network, with each runtime config hydrated. Rows are fully read and the cursor
// closed before hydration because the store uses a single SQLite connection.
func (s *Store) ListVNFs(ctx context.Context, networkID *int64) ([]VNF, error) {
	query := vnfIdentitySelect
	args := make([]any, 0, 1)
	if networkID != nil {
		query += ` WHERE f.network_id = ?`
		args = append(args, *networkID)
	}
	query += ` ORDER BY f.name COLLATE NOCASE, f.id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query vnfs: %w", err)
	}
	vnfs := make([]VNF, 0)
	for rows.Next() {
		vnf, err := scanVNFIdentity(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan vnf: %w", err)
		}
		vnfs = append(vnfs, vnf)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate vnfs: %w", err)
	}
	rows.Close()

	for i := range vnfs {
		if err := s.hydrateVNF(ctx, &vnfs[i]); err != nil {
			return nil, err
		}
	}
	return vnfs, nil
}

// ListVNFsByNetwork returns the network functions attached to one network.
func (s *Store) ListVNFsByNetwork(ctx context.Context, networkID int64) ([]VNF, error) {
	return s.ListVNFs(ctx, &networkID)
}

// UpdateVNFIdentity updates only the shared identity fields of a VNF: name,
// enabled, and the tap/memif metadata that activation provisions. It leaves the
// typed parent and child rows untouched.
func (s *Store) UpdateVNFIdentity(ctx context.Context, vnf VNF) (VNF, error) {
	now := time.Now().UTC().UnixMilli()
	if _, err := s.db.ExecContext(ctx, vnfIdentityUpdate,
		vnf.Name, vnf.Enabled, vnf.TapSwIfIndex, vnf.TapHostName,
		vnf.MemifSwIfIndex, vnf.MemifSocket, vnf.MemifHostName, now, vnf.ID,
	); err != nil {
		if isUniqueViolation(err) {
			return VNF{}, fmt.Errorf("%w: %q", ErrVNFNameTaken, vnf.Name)
		}
		return VNF{}, fmt.Errorf("update vnf identity: %w", err)
	}
	return s.GetVNF(ctx, vnf.ID)
}

// DeleteVNF removes a network function identity row. Typed parent and child
// rows are removed by the foreign-key cascade.
func (s *Store) DeleteVNF(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM vnfs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete vnf: %w", err)
	}
	return rowsAffectedOrNotFound(res, ErrVNFNotFound)
}

// rowsAffectedOrNotFound returns notFound when the statement matched no rows.
func rowsAffectedOrNotFound(res sql.Result, notFound error) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return notFound
	}
	return nil
}

// requireVNFType returns ErrVNFNotFound when the VNF is missing and
// ErrVNFTypeMismatch when it is not one of types.
func (s *Store) requireVNFType(ctx context.Context, id int64, types ...string) error {
	var vnfType string
	err := s.db.QueryRowContext(ctx, `SELECT type FROM vnfs WHERE id = ?`, id).Scan(&vnfType)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrVNFNotFound
	}
	if err != nil {
		return fmt.Errorf("read vnf type: %w", err)
	}
	for _, allowed := range types {
		if vnfType == allowed {
			return nil
		}
	}
	return ErrVNFTypeMismatch
}

// insertVNFIdentity inserts the shared identity row inside tx and returns its ID.
func insertVNFIdentity(ctx context.Context, tx *sql.Tx, name string, networkID int64, vnfType string, enabled bool, now int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO vnfs (name, network_id, type, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		name, networkID, vnfType, enabled, now, now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read inserted vnf id: %w", err)
	}
	return id, nil
}

// classifyVNFInsertError maps a failed identity insert to a sentinel conflict.
func (s *Store) classifyVNFInsertError(ctx context.Context, err error, name string) error {
	if !isUniqueViolation(err) {
		return fmt.Errorf("insert vnf: %w", err)
	}
	var id int64
	queryErr := s.db.QueryRowContext(ctx, `SELECT id FROM vnfs WHERE name = ?`, name).Scan(&id)
	if queryErr == nil {
		return fmt.Errorf("%w: %q", ErrVNFNameTaken, name)
	}
	if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
		return fmt.Errorf("classify vnf insert conflict: %w", queryErr)
	}
	return ErrVNFTypeTaken
}

// checkVNFFamily validates the DHCP family a create or update targets.
func checkVNFFamily(family string) error {
	if family != VNFTypeDHCPv4 && family != VNFTypeDHCPv6 {
		return fmt.Errorf("unsupported dhcp family %q", family)
	}
	return nil
}

// CreateDHCPVNF inserts a DHCPv4 or DHCPv6 VNF identity and typed parent row in
// one transaction.
func (s *Store) CreateDHCPVNF(ctx context.Context, in NewDHCPVNF) (VNF, error) {
	if err := checkVNFFamily(in.Family); err != nil {
		return VNF{}, err
	}
	if _, err := s.GetNetwork(ctx, in.NetworkID); err != nil {
		return VNF{}, err
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VNF{}, fmt.Errorf("begin vnf create: %w", err)
	}
	defer tx.Rollback()

	id, err := insertVNFIdentity(ctx, tx, in.Name, in.NetworkID, in.Family, in.Enabled, now)
	if err != nil {
		_ = tx.Rollback()
		return VNF{}, s.classifyVNFInsertError(ctx, err, in.Name)
	}
	cfg := in.Config
	if in.Family == VNFTypeDHCPv4 {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO dhcpv4_vnfs
				(vnf_id, gateway, range_start, range_end, lease_time, dns, bind_ip,
				 transport, dns_vnf_id, dns_domain, dns_ttl)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, 0), ?, ?)`,
			id, cfg.Gateway, cfg.RangeStart, cfg.RangeEnd, cfg.LeaseTime, cfg.DNS,
			cfg.BindIP, cfg.Transport, cfg.DNSVNFID, cfg.DNSDomain, cfg.DNSTTL,
		)
	} else {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO dhcpv6_vnfs
				(vnf_id, gateway, range_start, range_end, lease_time, dns, bind_ip,
				 transport, dns_vnf_id, dns_domain, dns_ttl)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, 0), ?, ?)`,
			id, cfg.Gateway, cfg.RangeStart, cfg.RangeEnd, cfg.LeaseTime, cfg.DNS,
			cfg.BindIP, cfg.Transport, cfg.DNSVNFID, cfg.DNSDomain, cfg.DNSTTL,
		)
	}
	if err != nil {
		return VNF{}, fmt.Errorf("insert dhcp vnf: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return VNF{}, fmt.Errorf("commit vnf create: %w", err)
	}
	return s.GetVNF(ctx, id)
}

// UpdateDHCPVNF updates a DHCP VNF identity and its typed parent row.
func (s *Store) UpdateDHCPVNF(ctx context.Context, vnf VNF, cfg DHCPVNFConfig) (VNF, error) {
	if err := checkVNFFamily(vnf.Type); err != nil {
		return VNF{}, err
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VNF{}, fmt.Errorf("begin vnf update: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, vnfIdentityUpdate,
		vnf.Name, vnf.Enabled, vnf.TapSwIfIndex, vnf.TapHostName,
		vnf.MemifSwIfIndex, vnf.MemifSocket, vnf.MemifHostName, now, vnf.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return VNF{}, fmt.Errorf("%w: %q", ErrVNFNameTaken, vnf.Name)
		}
		return VNF{}, fmt.Errorf("update vnf identity: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return VNF{}, err
	}
	table := "dhcpv4_vnfs"
	if vnf.Type == VNFTypeDHCPv6 {
		table = "dhcpv6_vnfs"
	}
	res, err = tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET gateway = ?, range_start = ?, range_end = ?, lease_time = ?,
		    dns = ?, bind_ip = ?, transport = ?, dns_vnf_id = NULLIF(?, 0),
		    dns_domain = ?, dns_ttl = ?
		WHERE vnf_id = ?`, table),
		cfg.Gateway, cfg.RangeStart, cfg.RangeEnd, cfg.LeaseTime, cfg.DNS,
		cfg.BindIP, cfg.Transport, cfg.DNSVNFID, cfg.DNSDomain, cfg.DNSTTL,
		vnf.ID,
	)
	if err != nil {
		return VNF{}, fmt.Errorf("update dhcp vnf: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return VNF{}, err
	}
	if err := tx.Commit(); err != nil {
		return VNF{}, fmt.Errorf("commit vnf update: %w", err)
	}
	return s.GetVNF(ctx, vnf.ID)
}

// SetDHCPVNFNetworkBindings writes the derived gateway and bind address of a
// DHCPv4 VNF. It is the only write path for the values the API derives from
// shared-memif and NAT44 setup.
func (s *Store) SetDHCPVNFNetworkBindings(ctx context.Context, id int64, gateway, bindIP string) error {
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin vnf binding update: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE dhcpv4_vnfs SET gateway = ?, bind_ip = ? WHERE vnf_id = ?`,
		gateway, bindIP, id,
	)
	if err != nil {
		return fmt.Errorf("update dhcp vnf bindings: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vnfs SET updated_at = ? WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("touch vnf: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vnf binding update: %w", err)
	}
	return nil
}

// CreateDNSVNF inserts a DNS VNF identity and typed parent row.
func (s *Store) CreateDNSVNF(ctx context.Context, in NewDNSVNF) (VNF, error) {
	if _, err := s.GetNetwork(ctx, in.NetworkID); err != nil {
		return VNF{}, err
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VNF{}, fmt.Errorf("begin vnf create: %w", err)
	}
	defer tx.Rollback()
	id, err := insertVNFIdentity(ctx, tx, in.Name, in.NetworkID, VNFTypeDNS, in.Enabled, now)
	if err != nil {
		_ = tx.Rollback()
		return VNF{}, s.classifyVNFInsertError(ctx, err, in.Name)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dns_vnfs (vnf_id, forward) VALUES (?, ?)`, id, in.Config.Forward); err != nil {
		return VNF{}, fmt.Errorf("insert dns vnf: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return VNF{}, fmt.Errorf("commit vnf create: %w", err)
	}
	return s.GetVNF(ctx, id)
}

// UpdateDNSVNF updates a DNS VNF identity and its typed parent row.
func (s *Store) UpdateDNSVNF(ctx context.Context, vnf VNF, cfg DNSVNFConfig) (VNF, error) {
	if vnf.Type != VNFTypeDNS {
		return VNF{}, ErrVNFTypeMismatch
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VNF{}, fmt.Errorf("begin vnf update: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, vnfIdentityUpdate,
		vnf.Name, vnf.Enabled, vnf.TapSwIfIndex, vnf.TapHostName,
		vnf.MemifSwIfIndex, vnf.MemifSocket, vnf.MemifHostName, now, vnf.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return VNF{}, fmt.Errorf("%w: %q", ErrVNFNameTaken, vnf.Name)
		}
		return VNF{}, fmt.Errorf("update vnf identity: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return VNF{}, err
	}
	res, err = tx.ExecContext(ctx, `UPDATE dns_vnfs SET forward = ? WHERE vnf_id = ?`, cfg.Forward, vnf.ID)
	if err != nil {
		return VNF{}, fmt.Errorf("update dns vnf: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return VNF{}, err
	}
	if err := tx.Commit(); err != nil {
		return VNF{}, fmt.Errorf("commit vnf update: %w", err)
	}
	return s.GetVNF(ctx, vnf.ID)
}

// CreateNAT44VNF inserts a NAT44 VNF identity and typed parent row.
func (s *Store) CreateNAT44VNF(ctx context.Context, in NewNAT44VNF) (VNF, error) {
	if _, err := s.GetNetwork(ctx, in.NetworkID); err != nil {
		return VNF{}, err
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VNF{}, fmt.Errorf("begin vnf create: %w", err)
	}
	defer tx.Rollback()
	id, err := insertVNFIdentity(ctx, tx, in.Name, in.NetworkID, VNFTypeNAT44, in.Enabled, now)
	if err != nil {
		_ = tx.Rollback()
		return VNF{}, s.classifyVNFInsertError(ctx, err, in.Name)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO nat44_vnfs
			(vnf_id, gateway, outside_interface, outside_address, outside_gateway, nat44_pool)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, in.Config.Gateway, in.Config.OutsideInterface, in.Config.OutsideAddress,
		in.Config.OutsideGateway, in.Config.NAT44Pool,
	); err != nil {
		return VNF{}, fmt.Errorf("insert nat44 vnf: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return VNF{}, fmt.Errorf("commit vnf create: %w", err)
	}
	return s.GetVNF(ctx, id)
}

// UpdateNAT44VNF updates a NAT44 VNF identity and its typed parent row.
func (s *Store) UpdateNAT44VNF(ctx context.Context, vnf VNF, cfg NAT44VNFConfig) (VNF, error) {
	if vnf.Type != VNFTypeNAT44 {
		return VNF{}, ErrVNFTypeMismatch
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VNF{}, fmt.Errorf("begin vnf update: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, vnfIdentityUpdate,
		vnf.Name, vnf.Enabled, vnf.TapSwIfIndex, vnf.TapHostName,
		vnf.MemifSwIfIndex, vnf.MemifSocket, vnf.MemifHostName, now, vnf.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return VNF{}, fmt.Errorf("%w: %q", ErrVNFNameTaken, vnf.Name)
		}
		return VNF{}, fmt.Errorf("update vnf identity: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return VNF{}, err
	}
	res, err = tx.ExecContext(ctx, `
		UPDATE nat44_vnfs
		SET gateway = ?, outside_interface = ?, outside_address = ?,
		    outside_gateway = ?, nat44_pool = ?
		WHERE vnf_id = ?`,
		cfg.Gateway, cfg.OutsideInterface, cfg.OutsideAddress, cfg.OutsideGateway,
		cfg.NAT44Pool, vnf.ID,
	)
	if err != nil {
		return VNF{}, fmt.Errorf("update nat44 vnf: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return VNF{}, err
	}
	if err := tx.Commit(); err != nil {
		return VNF{}, fmt.Errorf("commit vnf update: %w", err)
	}
	return s.GetVNF(ctx, vnf.ID)
}

// normalizeBGPStorageConfig applies the two BGP defaults the typed table
// requires (a transport and a multihop TTL) so a direct store write cannot
// violate the schema constraints.
func normalizeBGPStorageConfig(cfg *BGPVNFConfig) {
	if cfg.Transport == "" {
		cfg.Transport = "memif"
	}
	if cfg.MultihopTTL == 0 {
		cfg.MultihopTTL = 64
	}
}

// CreateBGPVNF inserts a BGP VNF identity and typed parent row.
func (s *Store) CreateBGPVNF(ctx context.Context, in NewBGPVNF) (VNF, error) {
	if _, err := s.GetNetwork(ctx, in.NetworkID); err != nil {
		return VNF{}, err
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VNF{}, fmt.Errorf("begin vnf create: %w", err)
	}
	defer tx.Rollback()
	id, err := insertVNFIdentity(ctx, tx, in.Name, in.NetworkID, VNFTypeBGP, in.Enabled, now)
	if err != nil {
		_ = tx.Rollback()
		return VNF{}, s.classifyVNFInsertError(ctx, err, in.Name)
	}
	cfg := in.Config
	normalizeBGPStorageConfig(&cfg)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bgp_vnfs
			(vnf_id, transport, local_as, router_id, peer_as, peer_ip, local_ip,
			 peer_gateway, multihop_ttl, password, passive, target_table_id, next_hop)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, cfg.Transport, cfg.LocalAS, cfg.RouterID, cfg.PeerAS, cfg.PeerIP,
		cfg.LocalIP, cfg.PeerGateway, cfg.MultihopTTL, cfg.Password, cfg.Passive,
		cfg.TargetTableID, cfg.NextHop,
	); err != nil {
		return VNF{}, fmt.Errorf("insert bgp vnf: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return VNF{}, fmt.Errorf("commit vnf create: %w", err)
	}
	return s.GetVNF(ctx, id)
}

// UpdateBGPVNF updates a BGP VNF identity and its typed parent row. An empty
// password clears the stored one; the password is never read back.
func (s *Store) UpdateBGPVNF(ctx context.Context, vnf VNF, cfg BGPVNFConfig) (VNF, error) {
	if vnf.Type != VNFTypeBGP {
		return VNF{}, ErrVNFTypeMismatch
	}
	normalizeBGPStorageConfig(&cfg)
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VNF{}, fmt.Errorf("begin vnf update: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, vnfIdentityUpdate,
		vnf.Name, vnf.Enabled, vnf.TapSwIfIndex, vnf.TapHostName,
		vnf.MemifSwIfIndex, vnf.MemifSocket, vnf.MemifHostName, now, vnf.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return VNF{}, fmt.Errorf("%w: %q", ErrVNFNameTaken, vnf.Name)
		}
		return VNF{}, fmt.Errorf("update vnf identity: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return VNF{}, err
	}
	res, err = tx.ExecContext(ctx, `
		UPDATE bgp_vnfs
		SET transport = ?, local_as = ?, router_id = ?, peer_as = ?, peer_ip = ?,
		    local_ip = ?, peer_gateway = ?, multihop_ttl = ?, password = ?,
		    passive = ?, target_table_id = ?, next_hop = ?
		WHERE vnf_id = ?`,
		cfg.Transport, cfg.LocalAS, cfg.RouterID, cfg.PeerAS, cfg.PeerIP,
		cfg.LocalIP, cfg.PeerGateway, cfg.MultihopTTL, cfg.Password, cfg.Passive,
		cfg.TargetTableID, cfg.NextHop, vnf.ID,
	)
	if err != nil {
		return VNF{}, fmt.Errorf("update bgp vnf: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFNotFound); err != nil {
		return VNF{}, err
	}
	if err := tx.Commit(); err != nil {
		return VNF{}, fmt.Errorf("commit vnf update: %w", err)
	}
	return s.GetVNF(ctx, vnf.ID)
}

// typedVNFType returns the stored type of id or a not-found error.
func (s *Store) typedVNFType(ctx context.Context, id int64) (string, error) {
	var vnfType string
	err := s.db.QueryRowContext(ctx, `SELECT type FROM vnfs WHERE id = ?`, id).Scan(&vnfType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrVNFNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read vnf type: %w", err)
	}
	return vnfType, nil
}

// GetDHCPVNF returns one DHCPv4 or DHCPv6 VNF.
func (s *Store) GetDHCPVNF(ctx context.Context, id int64) (DHCPVNF, error) {
	vnfType, err := s.typedVNFType(ctx, id)
	if err != nil {
		return DHCPVNF{}, err
	}
	if vnfType != VNFTypeDHCPv4 && vnfType != VNFTypeDHCPv6 {
		return DHCPVNF{}, ErrVNFTypeMismatch
	}
	vnfs, err := s.listDHCPVNFs(ctx, nil, "", &id)
	if err != nil {
		return DHCPVNF{}, err
	}
	if len(vnfs) == 0 {
		return DHCPVNF{}, ErrVNFNotFound
	}
	return vnfs[0], nil
}

// ListDHCPVNFs returns DHCP VNFs, optionally filtered by network and family.
func (s *Store) ListDHCPVNFs(ctx context.Context, networkID *int64, family string) ([]DHCPVNF, error) {
	if family != "" && family != VNFTypeDHCPv4 && family != VNFTypeDHCPv6 {
		return nil, fmt.Errorf("unsupported dhcp family %q", family)
	}
	return s.listDHCPVNFs(ctx, networkID, family, nil)
}

func (s *Store) listDHCPVNFs(ctx context.Context, networkID *int64, family string, id *int64) ([]DHCPVNF, error) {
	query := `
		SELECT f.id, f.name, f.network_id, n.name, f.type, f.enabled,
		       COALESCE(c4.gateway, c6.gateway), COALESCE(c4.range_start, c6.range_start),
		       COALESCE(c4.range_end, c6.range_end), COALESCE(c4.lease_time, c6.lease_time),
		       COALESCE(c4.dns, c6.dns), COALESCE(c4.bind_ip, c6.bind_ip),
		       COALESCE(c4.transport, c6.transport), COALESCE(c4.dns_vnf_id, c6.dns_vnf_id, 0),
		       COALESCE(c4.dns_domain, c6.dns_domain), COALESCE(c4.dns_ttl, c6.dns_ttl),
		       (SELECT COUNT(*) FROM dhcpv4_static_assignments a WHERE a.vnf_id = f.id),
		       (SELECT COUNT(*) FROM dhcpv4_options o WHERE o.vnf_id = f.id) +
		       (SELECT COUNT(*) FROM dhcpv6_options o WHERE o.vnf_id = f.id),
		       f.tap_sw_if_index, f.tap_host_name, f.created_at, f.updated_at
		FROM vnfs AS f
		JOIN networks AS n ON n.id = f.network_id
		LEFT JOIN dhcpv4_vnfs AS c4 ON c4.vnf_id = f.id
		LEFT JOIN dhcpv6_vnfs AS c6 ON c6.vnf_id = f.id
		WHERE f.type IN ('dhcpv4', 'dhcpv6')`
	args := make([]any, 0, 2)
	if networkID != nil {
		query += ` AND f.network_id = ?`
		args = append(args, *networkID)
	}
	if family != "" {
		query += ` AND f.type = ?`
		args = append(args, family)
	}
	if id != nil {
		query += ` AND f.id = ?`
		args = append(args, *id)
	}
	query += ` ORDER BY f.name COLLATE NOCASE, f.id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query dhcp vnfs: %w", err)
	}
	defer rows.Close()
	out := make([]DHCPVNF, 0)
	for rows.Next() {
		var (
			vnf       DHCPVNF
			enabled   int64
			dnsVNFID  sql.NullInt64
			tapSwIf   int64
			createdAt int64
			updatedAt int64
		)
		if err := rows.Scan(
			&vnf.ID, &vnf.Name, &vnf.NetworkID, &vnf.NetworkName, &vnf.Family,
			&enabled, &vnf.Config.Gateway, &vnf.Config.RangeStart,
			&vnf.Config.RangeEnd, &vnf.Config.LeaseTime, &vnf.Config.DNS,
			&vnf.Config.BindIP, &vnf.Config.Transport, &dnsVNFID,
			&vnf.Config.DNSDomain, &vnf.Config.DNSTTL, &vnf.StaticAssignmentCount,
			&vnf.OptionCount, &tapSwIf, &vnf.TapHostName, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan dhcp vnf: %w", err)
		}
		vnf.Enabled = enabled != 0
		vnf.Config.DNSVNFID = dnsVNFID.Int64
		vnf.TapSwIfIndex = uint32(tapSwIf)
		vnf.CreatedAt = time.UnixMilli(createdAt).UTC()
		vnf.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		out = append(out, vnf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dhcp vnfs: %w", err)
	}
	return out, nil
}

// GetDNSVNF returns one DNS VNF.
func (s *Store) GetDNSVNF(ctx context.Context, id int64) (DNSVNF, error) {
	vnfType, err := s.typedVNFType(ctx, id)
	if err != nil {
		return DNSVNF{}, err
	}
	if vnfType != VNFTypeDNS {
		return DNSVNF{}, ErrVNFTypeMismatch
	}
	vnfs, err := s.listDNSVNFs(ctx, nil, &id)
	if err != nil {
		return DNSVNF{}, err
	}
	if len(vnfs) == 0 {
		return DNSVNF{}, ErrVNFNotFound
	}
	return vnfs[0], nil
}

// ListDNSVNFs returns DNS VNFs, optionally filtered by network.
func (s *Store) ListDNSVNFs(ctx context.Context, networkID *int64) ([]DNSVNF, error) {
	return s.listDNSVNFs(ctx, networkID, nil)
}

func (s *Store) listDNSVNFs(ctx context.Context, networkID *int64, id *int64) ([]DNSVNF, error) {
	query := `
		SELECT f.id, f.name, f.network_id, n.name, f.enabled, c.forward,
		       (SELECT COUNT(*) FROM dns_records r WHERE r.vnf_id = f.id),
		       (SELECT COUNT(*) FROM dns_forwarders w WHERE w.vnf_id = f.id),
		       f.tap_sw_if_index, f.tap_host_name, f.created_at, f.updated_at
		FROM vnfs AS f
		JOIN networks AS n ON n.id = f.network_id
		JOIN dns_vnfs AS c ON c.vnf_id = f.id
		WHERE f.type = 'dns'`
	args := make([]any, 0, 1)
	if networkID != nil {
		query += ` AND f.network_id = ?`
		args = append(args, *networkID)
	}
	if id != nil {
		query += ` AND f.id = ?`
		args = append(args, *id)
	}
	query += ` ORDER BY f.name COLLATE NOCASE, f.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query dns vnfs: %w", err)
	}
	defer rows.Close()
	out := make([]DNSVNF, 0)
	for rows.Next() {
		var (
			vnf       DNSVNF
			enabled   int64
			forward   int64
			tapSwIf   int64
			createdAt int64
			updatedAt int64
		)
		if err := rows.Scan(
			&vnf.ID, &vnf.Name, &vnf.NetworkID, &vnf.NetworkName, &enabled,
			&forward, &vnf.RecordCount, &vnf.ForwarderCount, &tapSwIf,
			&vnf.TapHostName, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan dns vnf: %w", err)
		}
		vnf.Enabled = enabled != 0
		vnf.Config.Forward = forward != 0
		vnf.TapSwIfIndex = uint32(tapSwIf)
		vnf.CreatedAt = time.UnixMilli(createdAt).UTC()
		vnf.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		out = append(out, vnf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dns vnfs: %w", err)
	}
	return out, nil
}

// GetNAT44VNF returns one NAT44 VNF.
func (s *Store) GetNAT44VNF(ctx context.Context, id int64) (NAT44VNF, error) {
	vnfType, err := s.typedVNFType(ctx, id)
	if err != nil {
		return NAT44VNF{}, err
	}
	if vnfType != VNFTypeNAT44 {
		return NAT44VNF{}, ErrVNFTypeMismatch
	}
	vnfs, err := s.listNAT44VNFs(ctx, nil, &id)
	if err != nil {
		return NAT44VNF{}, err
	}
	if len(vnfs) == 0 {
		return NAT44VNF{}, ErrVNFNotFound
	}
	return vnfs[0], nil
}

// ListNAT44VNFs returns NAT44 VNFs, optionally filtered by network.
func (s *Store) ListNAT44VNFs(ctx context.Context, networkID *int64) ([]NAT44VNF, error) {
	return s.listNAT44VNFs(ctx, networkID, nil)
}

func (s *Store) listNAT44VNFs(ctx context.Context, networkID *int64, id *int64) ([]NAT44VNF, error) {
	query := `
		SELECT f.id, f.name, f.network_id, n.name, f.enabled, c.gateway,
		       c.outside_interface, c.outside_address, c.outside_gateway, c.nat44_pool,
		       (SELECT COUNT(*) FROM nat44_dnat_rules r WHERE r.vnf_id = f.id),
		       f.tap_sw_if_index, f.tap_host_name, f.created_at, f.updated_at
		FROM vnfs AS f
		JOIN networks AS n ON n.id = f.network_id
		JOIN nat44_vnfs AS c ON c.vnf_id = f.id
		WHERE f.type = 'nat44'`
	args := make([]any, 0, 1)
	if networkID != nil {
		query += ` AND f.network_id = ?`
		args = append(args, *networkID)
	}
	if id != nil {
		query += ` AND f.id = ?`
		args = append(args, *id)
	}
	query += ` ORDER BY f.name COLLATE NOCASE, f.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query nat44 vnfs: %w", err)
	}
	defer rows.Close()
	out := make([]NAT44VNF, 0)
	for rows.Next() {
		var (
			vnf       NAT44VNF
			enabled   int64
			tapSwIf   int64
			createdAt int64
			updatedAt int64
		)
		if err := rows.Scan(
			&vnf.ID, &vnf.Name, &vnf.NetworkID, &vnf.NetworkName, &enabled,
			&vnf.Config.Gateway, &vnf.Config.OutsideInterface,
			&vnf.Config.OutsideAddress, &vnf.Config.OutsideGateway,
			&vnf.Config.NAT44Pool, &vnf.DNATRuleCount, &tapSwIf,
			&vnf.TapHostName, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan nat44 vnf: %w", err)
		}
		vnf.Enabled = enabled != 0
		vnf.TapSwIfIndex = uint32(tapSwIf)
		vnf.CreatedAt = time.UnixMilli(createdAt).UTC()
		vnf.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		out = append(out, vnf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nat44 vnfs: %w", err)
	}
	return out, nil
}

// GetBGPVNF returns one BGP VNF. The password is never hydrated.
func (s *Store) GetBGPVNF(ctx context.Context, id int64) (BGPVNF, error) {
	vnfType, err := s.typedVNFType(ctx, id)
	if err != nil {
		return BGPVNF{}, err
	}
	if vnfType != VNFTypeBGP {
		return BGPVNF{}, ErrVNFTypeMismatch
	}
	vnfs, err := s.listBGPVNFs(ctx, nil, &id)
	if err != nil {
		return BGPVNF{}, err
	}
	if len(vnfs) == 0 {
		return BGPVNF{}, ErrVNFNotFound
	}
	return vnfs[0], nil
}

// ListBGPVNFs returns BGP VNFs, optionally filtered by network.
func (s *Store) ListBGPVNFs(ctx context.Context, networkID *int64) ([]BGPVNF, error) {
	return s.listBGPVNFs(ctx, networkID, nil)
}

func (s *Store) listBGPVNFs(ctx context.Context, networkID *int64, id *int64) ([]BGPVNF, error) {
	query := `
		SELECT f.id, f.name, f.network_id, n.name, f.enabled, c.transport,
		       c.local_as, c.router_id, c.peer_as, c.peer_ip, c.local_ip,
		       c.peer_gateway, c.multihop_ttl, c.passive, c.target_table_id,
		       c.next_hop,
		       (SELECT COUNT(*) FROM bgp_advertised_prefixes p WHERE p.vnf_id = f.id),
		       f.tap_sw_if_index, f.tap_host_name, f.created_at, f.updated_at
		FROM vnfs AS f
		JOIN networks AS n ON n.id = f.network_id
		JOIN bgp_vnfs AS c ON c.vnf_id = f.id
		WHERE f.type = 'bgp'`
	args := make([]any, 0, 1)
	if networkID != nil {
		query += ` AND f.network_id = ?`
		args = append(args, *networkID)
	}
	if id != nil {
		query += ` AND f.id = ?`
		args = append(args, *id)
	}
	query += ` ORDER BY f.name COLLATE NOCASE, f.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query bgp vnfs: %w", err)
	}
	defer rows.Close()
	out := make([]BGPVNF, 0)
	for rows.Next() {
		var (
			vnf       BGPVNF
			enabled   int64
			localAS   int64
			peerAS    int64
			ttl       int64
			passive   int64
			tableID   int64
			tapSwIf   int64
			createdAt int64
			updatedAt int64
		)
		if err := rows.Scan(
			&vnf.ID, &vnf.Name, &vnf.NetworkID, &vnf.NetworkName, &enabled,
			&vnf.Config.Transport, &localAS, &vnf.Config.RouterID, &peerAS,
			&vnf.Config.PeerIP, &vnf.Config.LocalIP, &vnf.Config.PeerGateway,
			&ttl, &passive, &tableID, &vnf.Config.NextHop,
			&vnf.AdvertisedPrefixCount, &tapSwIf, &vnf.TapHostName,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bgp vnf: %w", err)
		}
		vnf.Enabled = enabled != 0
		vnf.Config.LocalAS = uint32(localAS)
		vnf.Config.PeerAS = uint32(peerAS)
		vnf.Config.MultihopTTL = uint32(ttl)
		vnf.Config.Passive = passive != 0
		vnf.Config.TargetTableID = uint32(tableID)
		vnf.TapSwIfIndex = uint32(tapSwIf)
		vnf.CreatedAt = time.UnixMilli(createdAt).UTC()
		vnf.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		out = append(out, vnf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bgp vnfs: %w", err)
	}
	return out, nil
}
