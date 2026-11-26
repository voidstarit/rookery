package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// touchVNF updates the parent VNF timestamp after a child write.
func touchVNF(ctx context.Context, tx *sql.Tx, vnfID int64, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE vnfs SET updated_at = ? WHERE id = ?`, now, vnfID); err != nil {
		return fmt.Errorf("touch vnf: %w", err)
	}
	return nil
}

// beginChildWrite verifies the parent type and opens a write transaction.
func (s *Store) beginChildWrite(ctx context.Context, vnfID int64, types ...string) (*sql.Tx, error) {
	if err := s.requireVNFType(ctx, vnfID, types...); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin vnf child write: %w", err)
	}
	return tx, nil
}

// childInsertConflict wraps a unique violation as ErrVNFChildTaken.
func childInsertConflict(err error, detail string) error {
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: %s", ErrVNFChildTaken, detail)
	}
	return err
}

// ListDHCPStaticAssignments returns the static assignments of a DHCPv4 VNF.
func (s *Store) ListDHCPStaticAssignments(ctx context.Context, vnfID int64) ([]DHCPStaticAssignment, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeDHCPv4); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vnf_id, mac, ip FROM dhcpv4_static_assignments
		WHERE vnf_id = ? ORDER BY id`, vnfID)
	if err != nil {
		return nil, fmt.Errorf("query dhcp static assignments: %w", err)
	}
	defer rows.Close()
	out := make([]DHCPStaticAssignment, 0)
	for rows.Next() {
		var item DHCPStaticAssignment
		if err := rows.Scan(&item.ID, &item.VNFID, &item.MAC, &item.IP); err != nil {
			return nil, fmt.Errorf("scan dhcp static assignment: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dhcp static assignments: %w", err)
	}
	return out, nil
}

// GetDHCPStaticAssignment returns one static assignment of a DHCPv4 VNF.
func (s *Store) GetDHCPStaticAssignment(ctx context.Context, vnfID, id int64) (DHCPStaticAssignment, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeDHCPv4); err != nil {
		return DHCPStaticAssignment{}, err
	}
	var item DHCPStaticAssignment
	err := s.db.QueryRowContext(ctx, `
		SELECT id, vnf_id, mac, ip FROM dhcpv4_static_assignments
		WHERE vnf_id = ? AND id = ?`, vnfID, id).
		Scan(&item.ID, &item.VNFID, &item.MAC, &item.IP)
	if errors.Is(err, sql.ErrNoRows) {
		return DHCPStaticAssignment{}, ErrVNFChildNotFound
	}
	if err != nil {
		return DHCPStaticAssignment{}, fmt.Errorf("read dhcp static assignment: %w", err)
	}
	return item, nil
}

// CreateDHCPStaticAssignment inserts one static assignment and bumps the parent.
func (s *Store) CreateDHCPStaticAssignment(ctx context.Context, vnfID int64, item DHCPStaticAssignment) (DHCPStaticAssignment, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDHCPv4)
	if err != nil {
		return DHCPStaticAssignment{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO dhcpv4_static_assignments (vnf_id, mac, ip) VALUES (?, ?, ?)`,
		vnfID, item.MAC, item.IP)
	if err != nil {
		return DHCPStaticAssignment{}, childInsertConflict(err, "that MAC or IP is already assigned")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return DHCPStaticAssignment{}, fmt.Errorf("read inserted static assignment id: %w", err)
	}
	if err := touchVNF(ctx, tx, vnfID, now); err != nil {
		return DHCPStaticAssignment{}, err
	}
	if err := tx.Commit(); err != nil {
		return DHCPStaticAssignment{}, fmt.Errorf("commit static assignment: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// UpdateDHCPStaticAssignment replaces one static assignment and bumps the parent.
func (s *Store) UpdateDHCPStaticAssignment(ctx context.Context, vnfID, id int64, item DHCPStaticAssignment) (DHCPStaticAssignment, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDHCPv4)
	if err != nil {
		return DHCPStaticAssignment{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		UPDATE dhcpv4_static_assignments SET mac = ?, ip = ?
		WHERE vnf_id = ? AND id = ?`, item.MAC, item.IP, vnfID, id)
	if err != nil {
		return DHCPStaticAssignment{}, childInsertConflict(err, "that MAC or IP is already assigned")
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFChildNotFound); err != nil {
		return DHCPStaticAssignment{}, err
	}
	if err := touchVNF(ctx, tx, vnfID, now); err != nil {
		return DHCPStaticAssignment{}, err
	}
	if err := tx.Commit(); err != nil {
		return DHCPStaticAssignment{}, fmt.Errorf("commit static assignment: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// DeleteDHCPStaticAssignment removes one static assignment and bumps the parent.
func (s *Store) DeleteDHCPStaticAssignment(ctx context.Context, vnfID, id int64) error {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDHCPv4)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return deleteVNFChild(ctx, tx, "dhcpv4_static_assignments", vnfID, id)
}

// deleteVNFChild removes one scoped child row, bumps the parent, and commits.
func deleteVNFChild(ctx context.Context, tx *sql.Tx, table string, vnfID, id int64) error {
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE vnf_id = ? AND id = ?`, table), vnfID, id)
	if err != nil {
		return fmt.Errorf("delete vnf child: %w", err)
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFChildNotFound); err != nil {
		return err
	}
	if err := touchVNF(ctx, tx, vnfID, time.Now().UTC().UnixMilli()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vnf child delete: %w", err)
	}
	return nil
}

// ListDHCPOptions returns the extra options of a DHCP VNF.
func (s *Store) ListDHCPOptions(ctx context.Context, vnfID int64) ([]DHCPOption, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeDHCPv4, VNFTypeDHCPv6); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vnf_id, code, format, value
		FROM (
			SELECT id, vnf_id, code, format, value FROM dhcpv4_options WHERE vnf_id = ?
			UNION ALL
			SELECT id, vnf_id, code, format, value FROM dhcpv6_options WHERE vnf_id = ?
		) ORDER BY code, id`, vnfID, vnfID)
	if err != nil {
		return nil, fmt.Errorf("query dhcp options: %w", err)
	}
	defer rows.Close()
	out := make([]DHCPOption, 0)
	for rows.Next() {
		var (
			item DHCPOption
			code int64
		)
		if err := rows.Scan(&item.ID, &item.VNFID, &code, &item.Format, &item.Value); err != nil {
			return nil, fmt.Errorf("scan dhcp option: %w", err)
		}
		item.Code = uint16(code)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dhcp options: %w", err)
	}
	return out, nil
}

// dhcpOptionTable returns the option table for a DHCP VNF type.
func (s *Store) dhcpOptionTable(ctx context.Context, vnfID int64) (string, error) {
	vnfType, err := s.typedVNFType(ctx, vnfID)
	if err != nil {
		return "", err
	}
	switch vnfType {
	case VNFTypeDHCPv4:
		return "dhcpv4_options", nil
	case VNFTypeDHCPv6:
		return "dhcpv6_options", nil
	default:
		return "", ErrVNFTypeMismatch
	}
}

// GetDHCPOption returns one extra option of a DHCP VNF.
func (s *Store) GetDHCPOption(ctx context.Context, vnfID, id int64) (DHCPOption, error) {
	table, err := s.dhcpOptionTable(ctx, vnfID)
	if err != nil {
		return DHCPOption{}, err
	}
	var (
		item DHCPOption
		code int64
	)
	err = s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, vnf_id, code, format, value FROM %s
		WHERE vnf_id = ? AND id = ?`, table), vnfID, id).
		Scan(&item.ID, &item.VNFID, &code, &item.Format, &item.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return DHCPOption{}, ErrVNFChildNotFound
	}
	if err != nil {
		return DHCPOption{}, fmt.Errorf("read dhcp option: %w", err)
	}
	item.Code = uint16(code)
	return item, nil
}

// CreateDHCPOption inserts one option and bumps the parent.
func (s *Store) CreateDHCPOption(ctx context.Context, vnfID int64, item DHCPOption) (DHCPOption, error) {
	table, err := s.dhcpOptionTable(ctx, vnfID)
	if err != nil {
		return DHCPOption{}, err
	}
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDHCPv4, VNFTypeDHCPv6)
	if err != nil {
		return DHCPOption{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (vnf_id, code, format, value) VALUES (?, ?, ?, ?)`, table),
		vnfID, item.Code, item.Format, item.Value)
	if err != nil {
		return DHCPOption{}, childInsertConflict(err, "that option code is already configured")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return DHCPOption{}, fmt.Errorf("read inserted dhcp option id: %w", err)
	}
	if err := touchVNF(ctx, tx, vnfID, now); err != nil {
		return DHCPOption{}, err
	}
	if err := tx.Commit(); err != nil {
		return DHCPOption{}, fmt.Errorf("commit dhcp option: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// UpdateDHCPOption replaces one option and bumps the parent.
func (s *Store) UpdateDHCPOption(ctx context.Context, vnfID, id int64, item DHCPOption) (DHCPOption, error) {
	table, err := s.dhcpOptionTable(ctx, vnfID)
	if err != nil {
		return DHCPOption{}, err
	}
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDHCPv4, VNFTypeDHCPv6)
	if err != nil {
		return DHCPOption{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET code = ?, format = ?, value = ?
		WHERE vnf_id = ? AND id = ?`, table), item.Code, item.Format, item.Value, vnfID, id)
	if err != nil {
		return DHCPOption{}, childInsertConflict(err, "that option code is already configured")
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFChildNotFound); err != nil {
		return DHCPOption{}, err
	}
	if err := touchVNF(ctx, tx, vnfID, time.Now().UTC().UnixMilli()); err != nil {
		return DHCPOption{}, err
	}
	if err := tx.Commit(); err != nil {
		return DHCPOption{}, fmt.Errorf("commit dhcp option: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// DeleteDHCPOption removes one option and bumps the parent.
func (s *Store) DeleteDHCPOption(ctx context.Context, vnfID, id int64) error {
	table, err := s.dhcpOptionTable(ctx, vnfID)
	if err != nil {
		return err
	}
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDHCPv4, VNFTypeDHCPv6)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return deleteVNFChild(ctx, tx, table, vnfID, id)
}

// ListDNSVNFRecords returns the configured static records of a DNS VNF.
func (s *Store) ListDNSVNFRecords(ctx context.Context, vnfID int64) ([]DNSRecord, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeDNS); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vnf_id, name, type, value, ttl FROM dns_records
		WHERE vnf_id = ? ORDER BY id`, vnfID)
	if err != nil {
		return nil, fmt.Errorf("query dns records: %w", err)
	}
	defer rows.Close()
	out := make([]DNSRecord, 0)
	for rows.Next() {
		var item DNSRecord
		if err := rows.Scan(&item.ID, &item.VNFID, &item.Name, &item.Type, &item.Value, &item.TTL); err != nil {
			return nil, fmt.Errorf("scan dns record: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dns records: %w", err)
	}
	return out, nil
}

// GetDNSVNFRecord returns one configured record of a DNS VNF.
func (s *Store) GetDNSVNFRecord(ctx context.Context, vnfID, id int64) (DNSRecord, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeDNS); err != nil {
		return DNSRecord{}, err
	}
	var item DNSRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT id, vnf_id, name, type, value, ttl FROM dns_records
		WHERE vnf_id = ? AND id = ?`, vnfID, id).
		Scan(&item.ID, &item.VNFID, &item.Name, &item.Type, &item.Value, &item.TTL)
	if errors.Is(err, sql.ErrNoRows) {
		return DNSRecord{}, ErrVNFChildNotFound
	}
	if err != nil {
		return DNSRecord{}, fmt.Errorf("read dns record: %w", err)
	}
	return item, nil
}

// CreateDNSVNFRecord inserts one record and bumps the parent.
func (s *Store) CreateDNSVNFRecord(ctx context.Context, vnfID int64, item DNSRecord) (DNSRecord, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDNS)
	if err != nil {
		return DNSRecord{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO dns_records (vnf_id, name, type, value, ttl) VALUES (?, ?, ?, ?, ?)`,
		vnfID, item.Name, item.Type, item.Value, item.TTL)
	if err != nil {
		return DNSRecord{}, childInsertConflict(err, "that record already exists")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return DNSRecord{}, fmt.Errorf("read inserted dns record id: %w", err)
	}
	if err := touchVNF(ctx, tx, vnfID, now); err != nil {
		return DNSRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return DNSRecord{}, fmt.Errorf("commit dns record: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// UpdateDNSVNFRecord replaces one record and bumps the parent.
func (s *Store) UpdateDNSVNFRecord(ctx context.Context, vnfID, id int64, item DNSRecord) (DNSRecord, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDNS)
	if err != nil {
		return DNSRecord{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE dns_records SET name = ?, type = ?, value = ?, ttl = ?
		WHERE vnf_id = ? AND id = ?`, item.Name, item.Type, item.Value, item.TTL, vnfID, id)
	if err != nil {
		return DNSRecord{}, childInsertConflict(err, "that record already exists")
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFChildNotFound); err != nil {
		return DNSRecord{}, err
	}
	if err := touchVNF(ctx, tx, vnfID, time.Now().UTC().UnixMilli()); err != nil {
		return DNSRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return DNSRecord{}, fmt.Errorf("commit dns record: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// DeleteDNSVNFRecord removes one record and bumps the parent.
func (s *Store) DeleteDNSVNFRecord(ctx context.Context, vnfID, id int64) error {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDNS)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return deleteVNFChild(ctx, tx, "dns_records", vnfID, id)
}

// ListDNSVNFForwarders returns the forwarders of a DNS VNF ordered by position.
func (s *Store) ListDNSVNFForwarders(ctx context.Context, vnfID int64) ([]DNSForwarder, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeDNS); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vnf_id, address, position FROM dns_forwarders
		WHERE vnf_id = ? ORDER BY position, id`, vnfID)
	if err != nil {
		return nil, fmt.Errorf("query dns forwarders: %w", err)
	}
	defer rows.Close()
	out := make([]DNSForwarder, 0)
	for rows.Next() {
		var item DNSForwarder
		if err := rows.Scan(&item.ID, &item.VNFID, &item.Address, &item.Position); err != nil {
			return nil, fmt.Errorf("scan dns forwarder: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dns forwarders: %w", err)
	}
	return out, nil
}

// GetDNSVNFForwarder returns one forwarder of a DNS VNF.
func (s *Store) GetDNSVNFForwarder(ctx context.Context, vnfID, id int64) (DNSForwarder, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeDNS); err != nil {
		return DNSForwarder{}, err
	}
	var item DNSForwarder
	err := s.db.QueryRowContext(ctx, `
		SELECT id, vnf_id, address, position FROM dns_forwarders
		WHERE vnf_id = ? AND id = ?`, vnfID, id).
		Scan(&item.ID, &item.VNFID, &item.Address, &item.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return DNSForwarder{}, ErrVNFChildNotFound
	}
	if err != nil {
		return DNSForwarder{}, fmt.Errorf("read dns forwarder: %w", err)
	}
	return item, nil
}

// CreateDNSVNFForwarder inserts one forwarder and bumps the parent.
func (s *Store) CreateDNSVNFForwarder(ctx context.Context, vnfID int64, item DNSForwarder) (DNSForwarder, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDNS)
	if err != nil {
		return DNSForwarder{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO dns_forwarders (vnf_id, address, position) VALUES (?, ?, ?)`,
		vnfID, item.Address, item.Position)
	if err != nil {
		return DNSForwarder{}, childInsertConflict(err, "that forwarder already exists")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return DNSForwarder{}, fmt.Errorf("read inserted dns forwarder id: %w", err)
	}
	if err := touchVNF(ctx, tx, vnfID, now); err != nil {
		return DNSForwarder{}, err
	}
	if err := tx.Commit(); err != nil {
		return DNSForwarder{}, fmt.Errorf("commit dns forwarder: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// UpdateDNSVNFForwarder replaces one forwarder and bumps the parent.
func (s *Store) UpdateDNSVNFForwarder(ctx context.Context, vnfID, id int64, item DNSForwarder) (DNSForwarder, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDNS)
	if err != nil {
		return DNSForwarder{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE dns_forwarders SET address = ?, position = ?
		WHERE vnf_id = ? AND id = ?`, item.Address, item.Position, vnfID, id)
	if err != nil {
		return DNSForwarder{}, childInsertConflict(err, "that forwarder already exists")
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFChildNotFound); err != nil {
		return DNSForwarder{}, err
	}
	if err := touchVNF(ctx, tx, vnfID, time.Now().UTC().UnixMilli()); err != nil {
		return DNSForwarder{}, err
	}
	if err := tx.Commit(); err != nil {
		return DNSForwarder{}, fmt.Errorf("commit dns forwarder: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// DeleteDNSVNFForwarder removes one forwarder and bumps the parent.
func (s *Store) DeleteDNSVNFForwarder(ctx context.Context, vnfID, id int64) error {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeDNS)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return deleteVNFChild(ctx, tx, "dns_forwarders", vnfID, id)
}

// ListNAT44DNATRules returns the DNAT rules of a NAT44 VNF.
func (s *Store) ListNAT44DNATRules(ctx context.Context, vnfID int64) ([]DNATRule, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeNAT44); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vnf_id, protocol, external_port, local_ip, local_port
		FROM nat44_dnat_rules WHERE vnf_id = ? ORDER BY id`, vnfID)
	if err != nil {
		return nil, fmt.Errorf("query dnat rules: %w", err)
	}
	defer rows.Close()
	out := make([]DNATRule, 0)
	for rows.Next() {
		var item DNATRule
		if err := rows.Scan(&item.ID, &item.VNFID, &item.Protocol, &item.ExternalPort,
			&item.LocalIP, &item.LocalPort); err != nil {
			return nil, fmt.Errorf("scan dnat rule: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dnat rules: %w", err)
	}
	return out, nil
}

// GetNAT44DNATRule returns one DNAT rule of a NAT44 VNF.
func (s *Store) GetNAT44DNATRule(ctx context.Context, vnfID, id int64) (DNATRule, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeNAT44); err != nil {
		return DNATRule{}, err
	}
	var item DNATRule
	err := s.db.QueryRowContext(ctx, `
		SELECT id, vnf_id, protocol, external_port, local_ip, local_port
		FROM nat44_dnat_rules WHERE vnf_id = ? AND id = ?`, vnfID, id).
		Scan(&item.ID, &item.VNFID, &item.Protocol, &item.ExternalPort, &item.LocalIP, &item.LocalPort)
	if errors.Is(err, sql.ErrNoRows) {
		return DNATRule{}, ErrVNFChildNotFound
	}
	if err != nil {
		return DNATRule{}, fmt.Errorf("read dnat rule: %w", err)
	}
	return item, nil
}

// CreateNAT44DNATRule inserts one DNAT rule and bumps the parent.
func (s *Store) CreateNAT44DNATRule(ctx context.Context, vnfID int64, item DNATRule) (DNATRule, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeNAT44)
	if err != nil {
		return DNATRule{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO nat44_dnat_rules (vnf_id, protocol, external_port, local_ip, local_port)
		VALUES (?, ?, ?, ?, ?)`,
		vnfID, item.Protocol, item.ExternalPort, item.LocalIP, item.LocalPort)
	if err != nil {
		return DNATRule{}, childInsertConflict(err, "that protocol and external port are already used")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return DNATRule{}, fmt.Errorf("read inserted dnat rule id: %w", err)
	}
	if err := touchVNF(ctx, tx, vnfID, now); err != nil {
		return DNATRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return DNATRule{}, fmt.Errorf("commit dnat rule: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// UpdateNAT44DNATRule replaces one DNAT rule and bumps the parent.
func (s *Store) UpdateNAT44DNATRule(ctx context.Context, vnfID, id int64, item DNATRule) (DNATRule, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeNAT44)
	if err != nil {
		return DNATRule{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE nat44_dnat_rules
		SET protocol = ?, external_port = ?, local_ip = ?, local_port = ?
		WHERE vnf_id = ? AND id = ?`,
		item.Protocol, item.ExternalPort, item.LocalIP, item.LocalPort, vnfID, id)
	if err != nil {
		return DNATRule{}, childInsertConflict(err, "that protocol and external port are already used")
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFChildNotFound); err != nil {
		return DNATRule{}, err
	}
	if err := touchVNF(ctx, tx, vnfID, time.Now().UTC().UnixMilli()); err != nil {
		return DNATRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return DNATRule{}, fmt.Errorf("commit dnat rule: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// DeleteNAT44DNATRule removes one DNAT rule and bumps the parent.
func (s *Store) DeleteNAT44DNATRule(ctx context.Context, vnfID, id int64) error {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeNAT44)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return deleteVNFChild(ctx, tx, "nat44_dnat_rules", vnfID, id)
}

// ListBGPVNFAdvertisedPrefixes returns the advertised prefixes of a BGP VNF.
func (s *Store) ListBGPVNFAdvertisedPrefixes(ctx context.Context, vnfID int64) ([]BGPPrefix, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeBGP); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vnf_id, prefix FROM bgp_advertised_prefixes
		WHERE vnf_id = ? ORDER BY id`, vnfID)
	if err != nil {
		return nil, fmt.Errorf("query advertised prefixes: %w", err)
	}
	defer rows.Close()
	out := make([]BGPPrefix, 0)
	for rows.Next() {
		var item BGPPrefix
		if err := rows.Scan(&item.ID, &item.VNFID, &item.Prefix); err != nil {
			return nil, fmt.Errorf("scan advertised prefix: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate advertised prefixes: %w", err)
	}
	return out, nil
}

// GetBGPVNFAdvertisedPrefix returns one advertised prefix of a BGP VNF.
func (s *Store) GetBGPVNFAdvertisedPrefix(ctx context.Context, vnfID, id int64) (BGPPrefix, error) {
	if err := s.requireVNFType(ctx, vnfID, VNFTypeBGP); err != nil {
		return BGPPrefix{}, err
	}
	var item BGPPrefix
	err := s.db.QueryRowContext(ctx, `
		SELECT id, vnf_id, prefix FROM bgp_advertised_prefixes
		WHERE vnf_id = ? AND id = ?`, vnfID, id).
		Scan(&item.ID, &item.VNFID, &item.Prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return BGPPrefix{}, ErrVNFChildNotFound
	}
	if err != nil {
		return BGPPrefix{}, fmt.Errorf("read advertised prefix: %w", err)
	}
	return item, nil
}

// CreateBGPVNFAdvertisedPrefix inserts one advertised prefix and bumps the parent.
func (s *Store) CreateBGPVNFAdvertisedPrefix(ctx context.Context, vnfID int64, item BGPPrefix) (BGPPrefix, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeBGP)
	if err != nil {
		return BGPPrefix{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO bgp_advertised_prefixes (vnf_id, prefix) VALUES (?, ?)`,
		vnfID, item.Prefix)
	if err != nil {
		return BGPPrefix{}, childInsertConflict(err, "that prefix is already advertised")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return BGPPrefix{}, fmt.Errorf("read inserted advertised prefix id: %w", err)
	}
	if err := touchVNF(ctx, tx, vnfID, now); err != nil {
		return BGPPrefix{}, err
	}
	if err := tx.Commit(); err != nil {
		return BGPPrefix{}, fmt.Errorf("commit advertised prefix: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// UpdateBGPVNFAdvertisedPrefix replaces one advertised prefix and bumps the parent.
func (s *Store) UpdateBGPVNFAdvertisedPrefix(ctx context.Context, vnfID, id int64, item BGPPrefix) (BGPPrefix, error) {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeBGP)
	if err != nil {
		return BGPPrefix{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE bgp_advertised_prefixes SET prefix = ?
		WHERE vnf_id = ? AND id = ?`, item.Prefix, vnfID, id)
	if err != nil {
		return BGPPrefix{}, childInsertConflict(err, "that prefix is already advertised")
	}
	if err := rowsAffectedOrNotFound(res, ErrVNFChildNotFound); err != nil {
		return BGPPrefix{}, err
	}
	if err := touchVNF(ctx, tx, vnfID, time.Now().UTC().UnixMilli()); err != nil {
		return BGPPrefix{}, err
	}
	if err := tx.Commit(); err != nil {
		return BGPPrefix{}, fmt.Errorf("commit advertised prefix: %w", err)
	}
	item.ID = id
	item.VNFID = vnfID
	return item, nil
}

// DeleteBGPVNFAdvertisedPrefix removes one advertised prefix and bumps the parent.
func (s *Store) DeleteBGPVNFAdvertisedPrefix(ctx context.Context, vnfID, id int64) error {
	tx, err := s.beginChildWrite(ctx, vnfID, VNFTypeBGP)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return deleteVNFChild(ctx, tx, "bgp_advertised_prefixes", vnfID, id)
}
