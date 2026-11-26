package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// vnfMigrationVersion is the schema version that moves VNF configuration into
// typed tables.
const vnfMigrationVersion = 61

// legacyVNFConfig is the shape of the vnfs.config JSON object before migration
// 0061. It is used only by the read-only preflight.
type legacyVNFConfig struct {
	Gateway           string `json:"gateway"`
	RangeStart        string `json:"range_start"`
	RangeEnd          string `json:"range_end"`
	LeaseTime         string `json:"lease_time"`
	DNS               string `json:"dns"`
	BindIP            string `json:"bind_ip"`
	StaticAssignments []struct {
		MAC string `json:"mac"`
		IP  string `json:"ip"`
	} `json:"static_assignments"`
	OutsideInterface string `json:"outside_interface"`
	OutsideAddress   string `json:"outside_address"`
	OutsideGateway   string `json:"outside_gateway"`
	NAT44Pool        string `json:"nat44_pool"`
	DNATRules        []struct {
		Protocol     string `json:"protocol"`
		ExternalPort int    `json:"external_port"`
		LocalIP      string `json:"local_ip"`
		LocalPort    int    `json:"local_port"`
	} `json:"dnat_rules"`
	Transport string `json:"transport"`
	LocalAS   uint32 `json:"local_as"`
	RouterID  string `json:"router_id"`
	PeerAS    uint32 `json:"peer_as"`
	PeerIP    string `json:"peer_ip"`
	LocalIP   string `json:"local_ip"`
	// The peer gateway keeps its JSON name.
	PeerGateway       string   `json:"peer_gateway"`
	MultihopTTL       uint32   `json:"multihop_ttl"`
	Password          string   `json:"password"`
	Passive           bool     `json:"passive"`
	TargetTableID     uint32   `json:"target_table_id"`
	AdvertisePrefixes []string `json:"advertise_prefixes"`
	NextHop           string   `json:"next_hop"`
	DNSRecords        []struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value string `json:"value"`
		TTL   uint32 `json:"ttl"`
	} `json:"dns_records"`
	DNSForward    bool     `json:"dns_forward"`
	DNSForwarders []string `json:"dns_forwarders"`
	DHCPOptions   []struct {
		Code   uint16 `json:"code"`
		Format string `json:"format"`
		Value  string `json:"value"`
	} `json:"dhcp_options"`
	DNSVNFID  int64  `json:"dns_vnf_id"`
	DNSDomain string `json:"dns_domain"`
	DNSTTL    uint32 `json:"dns_ttl"`
}

// legacyVNFRow is one pre-migration VNF identity row.
type legacyVNFRow struct {
	ID        int64
	Name      string
	NetworkID int64
	Type      string
	Config    legacyVNFConfig
}

// preflightVNFMigration checks the data that migration 0061 will move while the
// database is still at the previous schema version. It is read-only: on a
// problem it names the VNF IDs and fields and the caller refuses to migrate.
// It also detects a database stamped with a pre-release 0061 that has neither
// the old config column removed nor the typed tables created.
func preflightVNFMigration(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if !schemaTableExists(ctx, db, "vnfs") {
		return nil
	}
	hasConfig := schemaTableHasColumn(ctx, db, "vnfs", "config")
	hasTypedTables := schemaTableExists(ctx, db, "dhcpv4_vnfs")

	if version >= vnfMigrationVersion {
		if hasConfig && !hasTypedTables {
			return fmt.Errorf("database reports schema version %d but still has vnfs.config and no typed VNF tables; "+
				"it carries a pre-release 0061 stamp. Restore the pre-upgrade backup before starting this build", version)
		}
		return nil
	}
	if !hasConfig {
		return fmt.Errorf("database schema version %d has no vnfs.config column; "+
			"it does not match the expected pre-0061 VNF schema. Restore the database backup", version)
	}
	if hasTypedTables {
		return fmt.Errorf("database schema version %d already has typed VNF tables but no migration recorded; "+
			"restore the database backup before starting this build", version)
	}

	rows, err := db.QueryContext(ctx, `SELECT id, name, network_id, type, config FROM vnfs ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read vnfs for migration preflight: %w", err)
	}
	legacy := make([]legacyVNFRow, 0)
	for rows.Next() {
		var (
			row    legacyVNFRow
			config string
		)
		if err := rows.Scan(&row.ID, &row.Name, &row.NetworkID, &row.Type, &config); err != nil {
			rows.Close()
			return fmt.Errorf("scan vnf for migration preflight: %w", err)
		}
		if err := decodeLegacyVNFConfig(config, &row.Config); err != nil {
			rows.Close()
			return preflightError([]string{fmt.Sprintf("vnf %d (%s): %v", row.ID, row.Name, err)})
		}
		legacy = append(legacy, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate vnfs for migration preflight: %w", err)
	}
	rows.Close()

	problems := validateLegacyVNFRows(legacy)
	if len(problems) > 0 {
		return preflightError(problems)
	}
	return nil
}

// preflightError formats the named problems as one operator-facing error.
func preflightError(problems []string) error {
	return fmt.Errorf("VNF migration preflight failed; fix these rows before upgrading:\n  - %s",
		strings.Join(problems, "\n  - "))
}

// decodeLegacyVNFConfig decodes one vnfs.config JSON object and rejects values
// that are not an object or that have the wrong field types.
func decodeLegacyVNFConfig(raw string, out *legacyVNFConfig) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return errors.New("config is empty; expected a JSON object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &object); err != nil || object == nil {
		return errors.New("config is not a valid JSON object")
	}
	if err := json.Unmarshal([]byte(trimmed), out); err != nil {
		return fmt.Errorf("config has an unexpected field type: %w", err)
	}
	return nil
}

// validateLegacyVNFRows reports every row that the typed schema cannot store
// without losing or weakening data.
func validateLegacyVNFRows(rows []legacyVNFRow) []string {
	problems := make([]string, 0)
	add := func(row legacyVNFRow, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vnf %d (%s): %s", row.ID, row.Name, fmt.Sprintf(format, args...)))
	}

	byID := make(map[int64]legacyVNFRow, len(rows))
	networkTypes := make(map[string]int64, len(rows))
	for _, row := range rows {
		if !ValidVNFType(row.Type) {
			add(row, "unsupported type %q", row.Type)
			continue
		}
		key := fmt.Sprintf("%d/%s", row.NetworkID, row.Type)
		if previous, exists := networkTypes[key]; exists {
			add(row, "network %d already has a %s VNF (vnf %d); one VNF of each type per network is allowed",
				row.NetworkID, row.Type, previous)
		} else {
			networkTypes[key] = row.ID
		}
		byID[row.ID] = row
	}

	for _, row := range rows {
		if !ValidVNFType(row.Type) {
			continue
		}
		problems = append(problems, validateLegacyVNFConfig(row)...)
	}

	for _, row := range rows {
		if !ValidVNFType(row.Type) {
			continue
		}
		if row.Type != VNFTypeDHCPv4 && row.Type != VNFTypeDHCPv6 {
			continue
		}
		if row.Config.DNSVNFID == 0 {
			continue
		}
		target, exists := byID[row.Config.DNSVNFID]
		switch {
		case !exists:
			add(row, "config.dns_vnf_id %d does not reference an existing VNF", row.Config.DNSVNFID)
		case target.Type != VNFTypeDNS:
			add(row, "config.dns_vnf_id %d references vnf %d of type %q, not a DNS VNF", row.Config.DNSVNFID, target.ID, target.Type)
		case target.NetworkID != row.NetworkID:
			add(row, "config.dns_vnf_id %d references a DNS VNF on another network", row.Config.DNSVNFID)
		}
	}
	return problems
}

// validateLegacyVNFConfig reports the type-specific problems of one row.
func validateLegacyVNFConfig(row legacyVNFRow) []string {
	problems := make([]string, 0)
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vnf %d (%s): %s", row.ID, row.Name, fmt.Sprintf(format, args...)))
	}
	cfg := row.Config

	unexpected := func(field string, present bool) {
		if present {
			add("%s is not supported for a %s VNF and would be dropped", field, row.Type)
		}
	}

	switch row.Type {
	case VNFTypeDHCPv4, VNFTypeDHCPv6:
		if strings.TrimSpace(cfg.Gateway) == "" {
			add("config.gateway is required")
		}
		if strings.TrimSpace(cfg.RangeStart) == "" || strings.TrimSpace(cfg.RangeEnd) == "" {
			add("config.range_start and config.range_end are required")
		}
		if row.Type == VNFTypeDHCPv4 {
			problems = append(problems, validateLegacyStaticAssignments(row)...)
		} else {
			unexpected("config.static_assignments", len(cfg.StaticAssignments) > 0)
		}
		// DNS records, forwarders, DNAT rules, and advertised prefixes belong to
		// other VNF types. Migration 0061 would drop them, so refuse them here
		// rather than lose data silently.
		unexpected("config.dnat_rules", len(cfg.DNATRules) > 0)
		unexpected("config.dns_records", len(cfg.DNSRecords) > 0)
		unexpected("config.dns_forwarders", len(cfg.DNSForwarders) > 0)
		unexpected("config.advertise_prefixes", len(cfg.AdvertisePrefixes) > 0)
		switch cfg.Transport {
		case "", "shared", "dedicated", "tap", "memif":
		default:
			add("config.transport %q is not supported", cfg.Transport)
		}
		if row.Type == VNFTypeDHCPv6 {
			switch cfg.Transport {
			case "", "tap":
			default:
				add("config.transport %q is not a DHCPv6 transport", cfg.Transport)
			}
		}
		if cfg.DNSTTL > 86400 {
			add("config.dns_ttl must be at most 86400")
		}
		problems = append(problems, validateLegacyDHCPOptions(row)...)

	case VNFTypeDNS:
		unexpected("config.static_assignments", len(cfg.StaticAssignments) > 0)
		unexpected("config.dhcp_options", len(cfg.DHCPOptions) > 0)
		unexpected("config.dnat_rules", len(cfg.DNATRules) > 0)
		unexpected("config.advertise_prefixes", len(cfg.AdvertisePrefixes) > 0)
		problems = append(problems, validateLegacyDNSRecords(row)...)
		problems = append(problems, validateLegacyDNSForwarders(row)...)

	case VNFTypeNAT44:
		if strings.TrimSpace(cfg.Gateway) == "" {
			add("config.gateway is required")
		}
		if strings.TrimSpace(cfg.OutsideInterface) == "" {
			add("config.outside_interface is required")
		}
		unexpected("config.static_assignments", len(cfg.StaticAssignments) > 0)
		unexpected("config.dhcp_options", len(cfg.DHCPOptions) > 0)
		unexpected("config.dns_records", len(cfg.DNSRecords) > 0)
		unexpected("config.dns_forwarders", len(cfg.DNSForwarders) > 0)
		unexpected("config.advertise_prefixes", len(cfg.AdvertisePrefixes) > 0)
		problems = append(problems, validateLegacyDNATRules(row)...)

	case VNFTypeBGP:
		if cfg.LocalAS == 0 {
			add("config.local_as is required")
		}
		if cfg.PeerAS == 0 {
			add("config.peer_as is required")
		}
		if strings.TrimSpace(cfg.RouterID) == "" {
			add("config.router_id is required")
		}
		if strings.TrimSpace(cfg.PeerIP) == "" {
			add("config.peer_ip is required")
		}
		if strings.TrimSpace(cfg.LocalIP) == "" {
			add("config.local_ip is required")
		}
		switch cfg.Transport {
		case "", "memif", "tap":
		default:
			add("config.transport %q is not a BGP transport", cfg.Transport)
		}
		if cfg.MultihopTTL > 255 {
			add("config.multihop_ttl must be at most 255")
		}
		unexpected("config.static_assignments", len(cfg.StaticAssignments) > 0)
		unexpected("config.dhcp_options", len(cfg.DHCPOptions) > 0)
		unexpected("config.dns_records", len(cfg.DNSRecords) > 0)
		unexpected("config.dns_forwarders", len(cfg.DNSForwarders) > 0)
		unexpected("config.dnat_rules", len(cfg.DNATRules) > 0)
		problems = append(problems, validateLegacyAdvertisedPrefixes(row)...)
	}
	return problems
}

// validateLegacyStaticAssignments checks the DHCPv4 child keys and required
// fields the typed table enforces.
func validateLegacyStaticAssignments(row legacyVNFRow) []string {
	problems := make([]string, 0)
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vnf %d (%s): %s", row.ID, row.Name, fmt.Sprintf(format, args...)))
	}
	seenMAC := make(map[string]int, len(row.Config.StaticAssignments))
	seenIP := make(map[string]int, len(row.Config.StaticAssignments))
	for i, assignment := range row.Config.StaticAssignments {
		if strings.TrimSpace(assignment.MAC) == "" {
			add("config.static_assignments[%d].mac is required", i)
		}
		if strings.TrimSpace(assignment.IP) == "" {
			add("config.static_assignments[%d].ip is required", i)
		}
		macKey := strings.ToLower(strings.TrimSpace(assignment.MAC))
		if previous, exists := seenMAC[macKey]; exists {
			add("config.static_assignments[%d].mac duplicates entry %d", i, previous)
		} else {
			seenMAC[macKey] = i
		}
		ipKey := strings.TrimSpace(assignment.IP)
		if previous, exists := seenIP[ipKey]; exists {
			add("config.static_assignments[%d].ip duplicates entry %d", i, previous)
		} else {
			seenIP[ipKey] = i
		}
	}
	return problems
}

// validateLegacyDHCPOptions checks option codes against the typed code range
// and rejects duplicate codes.
func validateLegacyDHCPOptions(row legacyVNFRow) []string {
	problems := make([]string, 0)
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vnf %d (%s): %s", row.ID, row.Name, fmt.Sprintf(format, args...)))
	}
	seen := make(map[uint16]int, len(row.Config.DHCPOptions))
	for i, option := range row.Config.DHCPOptions {
		if row.Type == VNFTypeDHCPv4 && option.Code > 254 {
			add("config.dhcp_options[%d].code %d must be between 1 and 254 for DHCPv4", i, option.Code)
		}
		if option.Code == 0 {
			add("config.dhcp_options[%d].code must be greater than zero", i)
		}
		if previous, exists := seen[option.Code]; exists {
			add("config.dhcp_options[%d].code %d duplicates entry %d", i, option.Code, previous)
		} else {
			seen[option.Code] = i
		}
	}
	return problems
}

// validateLegacyDNSRecords checks record types, TTL bounds, required fields, and
// the unique record key.
func validateLegacyDNSRecords(row legacyVNFRow) []string {
	problems := make([]string, 0)
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vnf %d (%s): %s", row.ID, row.Name, fmt.Sprintf(format, args...)))
	}
	seen := make(map[string]int, len(row.Config.DNSRecords))
	for i, record := range row.Config.DNSRecords {
		recordType := strings.ToUpper(strings.TrimSpace(record.Type))
		switch recordType {
		case "A", "AAAA", "CNAME", "TXT", "PTR":
		default:
			add("config.dns_records[%d].type %q is not A, AAAA, CNAME, TXT, or PTR", i, record.Type)
		}
		if strings.TrimSpace(record.Name) == "" {
			add("config.dns_records[%d].name is required", i)
		}
		if strings.TrimSpace(record.Value) == "" {
			add("config.dns_records[%d].value is required", i)
		}
		if record.TTL > 86400 {
			add("config.dns_records[%d].ttl must be at most 86400", i)
		}
		key := record.Name + "\x00" + recordType + "\x00" + record.Value
		if previous, exists := seen[key]; exists {
			add("config.dns_records[%d] duplicates entry %d", i, previous)
		} else {
			seen[key] = i
		}
	}
	return problems
}

// validateLegacyDNSForwarders checks required fields and duplicate addresses.
func validateLegacyDNSForwarders(row legacyVNFRow) []string {
	problems := make([]string, 0)
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vnf %d (%s): %s", row.ID, row.Name, fmt.Sprintf(format, args...)))
	}
	seen := make(map[string]int, len(row.Config.DNSForwarders))
	for i, raw := range row.Config.DNSForwarders {
		address := strings.TrimSpace(raw)
		if address == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(address); err != nil {
			// A bare address is valid; a bare host is not.
			if net.ParseIP(address) == nil {
				add("config.dns_forwarders[%d] %q is not an IP address or IP:port", i, raw)
			}
		}
		if previous, exists := seen[address]; exists {
			add("config.dns_forwarders[%d] duplicates entry %d", i, previous)
		} else {
			seen[address] = i
		}
	}
	return problems
}

// validateLegacyDNATRules checks protocol, port bounds, and the unique external
// key.
func validateLegacyDNATRules(row legacyVNFRow) []string {
	problems := make([]string, 0)
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vnf %d (%s): %s", row.ID, row.Name, fmt.Sprintf(format, args...)))
	}
	seen := make(map[string]int, len(row.Config.DNATRules))
	for i, rule := range row.Config.DNATRules {
		protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
		if protocol != "tcp" && protocol != "udp" {
			add("config.dnat_rules[%d].protocol %q must be tcp or udp", i, rule.Protocol)
		}
		if rule.ExternalPort < 1 || rule.ExternalPort > 65535 {
			add("config.dnat_rules[%d].external_port must be between 1 and 65535", i)
		}
		if rule.LocalPort < 1 || rule.LocalPort > 65535 {
			add("config.dnat_rules[%d].local_port must be between 1 and 65535", i)
		}
		if strings.TrimSpace(rule.LocalIP) == "" {
			add("config.dnat_rules[%d].local_ip is required", i)
		}
		key := fmt.Sprintf("%s/%d", protocol, rule.ExternalPort)
		if previous, exists := seen[key]; exists {
			add("config.dnat_rules[%d] duplicates the external port of entry %d", i, previous)
		} else {
			seen[key] = i
		}
	}
	return problems
}

// validateLegacyAdvertisedPrefixes checks required values and duplicate
// prefixes.
func validateLegacyAdvertisedPrefixes(row legacyVNFRow) []string {
	problems := make([]string, 0)
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vnf %d (%s): %s", row.ID, row.Name, fmt.Sprintf(format, args...)))
	}
	seen := make(map[string]int, len(row.Config.AdvertisePrefixes))
	for i, raw := range row.Config.AdvertisePrefixes {
		prefix := strings.TrimSpace(raw)
		if prefix == "" {
			add("config.advertise_prefixes[%d] is empty", i)
			continue
		}
		if previous, exists := seen[prefix]; exists {
			add("config.advertise_prefixes[%d] duplicates entry %d", i, previous)
		} else {
			seen[prefix] = i
		}
	}
	return problems
}

// schemaTableExists reports whether a table exists in the database.
func schemaTableExists(ctx context.Context, db *sql.DB, table string) bool {
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	return err == nil
}

// schemaTableHasColumn reports whether a table has a column.
func schemaTableHasColumn(ctx context.Context, db *sql.DB, table, column string) bool {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name      string
			columnTyp string
			notNull   int
			defaultV  sql.NullString
			primary   int
		)
		if err := rows.Scan(&cid, &name, &columnTyp, &notNull, &defaultV, &primary); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}
