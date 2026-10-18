package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const aclSelect = `
	SELECT id, name, title, description, rules, vpp_index, created_at, updated_at
	FROM acls`

// CreateACL inserts a named ACL definition.
func (s *Store) CreateACL(ctx context.Context, acl ACL) (ACL, error) {
	rules, err := json.Marshal(acl.Rules)
	if err != nil {
		return ACL{}, fmt.Errorf("encode ACL rules: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO acls (name, title, description, rules, vpp_index, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		acl.Name, acl.Title, acl.Description, string(rules), acl.VPPIndex, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ACL{}, fmt.Errorf("%w: %q", ErrACLNameTaken, acl.Name)
		}
		return ACL{}, fmt.Errorf("insert ACL: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ACL{}, fmt.Errorf("read inserted ACL id: %w", err)
	}
	return s.GetACL(ctx, id)
}

// ListACLs returns all ACL definitions ordered by name.
func (s *Store) ListACLs(ctx context.Context) ([]ACL, error) {
	rows, err := s.db.QueryContext(ctx, aclSelect+` ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("query ACLs: %w", err)
	}
	defer rows.Close()
	acls := make([]ACL, 0)
	for rows.Next() {
		acl, err := scanACL(rows)
		if err != nil {
			return nil, err
		}
		acls = append(acls, acl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ACLs: %w", err)
	}
	return acls, nil
}

// GetACL returns one ACL by ID.
func (s *Store) GetACL(ctx context.Context, id int64) (ACL, error) {
	row := s.db.QueryRowContext(ctx, aclSelect+` WHERE id = ?`, id)
	acl, err := scanACL(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ACL{}, ErrACLNotFound
	}
	if err != nil {
		return ACL{}, err
	}
	return acl, nil
}

// GetACLByName returns one ACL by its unique name.
func (s *Store) GetACLByName(ctx context.Context, name string) (ACL, error) {
	row := s.db.QueryRowContext(ctx, aclSelect+` WHERE name = ? COLLATE NOCASE`, name)
	acl, err := scanACL(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ACL{}, ErrACLNotFound
	}
	if err != nil {
		return ACL{}, err
	}
	return acl, nil
}

// UpdateACL replaces an ACL's editable fields and rules.
func (s *Store) UpdateACL(ctx context.Context, acl ACL) (ACL, error) {
	rules, err := json.Marshal(acl.Rules)
	if err != nil {
		return ACL{}, fmt.Errorf("encode ACL rules: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE acls SET name = ?, title = ?, description = ?, rules = ?, updated_at = ?
		WHERE id = ?`,
		acl.Name, acl.Title, acl.Description, string(rules), now, acl.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ACL{}, fmt.Errorf("%w: %q", ErrACLNameTaken, acl.Name)
		}
		return ACL{}, fmt.Errorf("update ACL: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ACL{}, fmt.Errorf("read updated ACL rows: %w", err)
	}
	if affected == 0 {
		return ACL{}, ErrACLNotFound
	}
	return s.GetACL(ctx, acl.ID)
}

// SetACLVPPIndex records the VPP ACL index for an ACL.
func (s *Store) SetACLVPPIndex(ctx context.Context, id int64, index uint32) (ACL, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE acls SET vpp_index = ? WHERE id = ?`, index, id)
	if err != nil {
		return ACL{}, fmt.Errorf("update ACL VPP index: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ACL{}, fmt.Errorf("read updated ACL rows: %w", err)
	}
	if affected == 0 {
		return ACL{}, ErrACLNotFound
	}
	return s.GetACL(ctx, id)
}

// DeleteACL removes an ACL if it is not assigned to any interface.
func (s *Store) DeleteACL(ctx context.Context, id int64) error {
	var interfaceID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM virtual_interfaces
		WHERE ingress_acl_id = ? OR egress_acl_id = ? LIMIT 1`, id, id,
	).Scan(&interfaceID)
	switch {
	case err == nil:
		return ErrACLInUse
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check ACL usage: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM acls WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrACLInUse
		}
		return fmt.Errorf("delete ACL: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted ACL rows: %w", err)
	}
	if affected == 0 {
		return ErrACLNotFound
	}
	return nil
}

// SetInterfaceACLs assigns ingress and/or egress ACLs to an interface.
func (s *Store) SetInterfaceACLs(ctx context.Context, id int64, ingress, egress *int64) (VirtualInterface, error) {
	if ingress != nil {
		if _, err := s.GetACL(ctx, *ingress); err != nil {
			return VirtualInterface{}, err
		}
	}
	if egress != nil {
		if _, err := s.GetACL(ctx, *egress); err != nil {
			return VirtualInterface{}, err
		}
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE virtual_interfaces SET ingress_acl_id = ?, egress_acl_id = ?, updated_at = ? WHERE id = ?`,
		ingress, egress, now, id,
	)
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("update interface ACLs: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return VirtualInterface{}, ErrInterfaceNotFound
	}
	return s.GetInterface(ctx, id)
}

func scanACL(s scanner) (ACL, error) {
	var (
		acl       ACL
		rules     string
		vppIndex  int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(&acl.ID, &acl.Name, &acl.Title, &acl.Description, &rules, &vppIndex, &createdAt, &updatedAt); err != nil {
		return ACL{}, err
	}
	if err := json.Unmarshal([]byte(rules), &acl.Rules); err != nil {
		return ACL{}, fmt.Errorf("parse ACL rules: %w", err)
	}
	acl.VPPIndex = uint32(vppIndex)
	acl.CreatedAt = time.UnixMilli(createdAt).UTC()
	acl.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return acl, nil
}
