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
	var assigned int64
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM interface_acls WHERE acl_id = ? LIMIT 1`, id,
	).Scan(&assigned)
	switch {
	case err == nil:
		return ErrACLInUse
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check ACL usage: %w", err)
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM virtual_interfaces
		WHERE ingress_acl_id = ? OR egress_acl_id = ? LIMIT 1`, id, id,
	).Scan(&assigned)
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

// SetInterfaceACLs replaces the ordered ingress and egress ACL lists assigned
// to an interface. The legacy single-ACL columns are kept in sync with the
// first entry of each list.
func (s *Store) SetInterfaceACLs(ctx context.Context, id int64, ingress, egress []int64) (VirtualInterface, error) {
	if _, err := s.GetInterface(ctx, id); err != nil {
		return VirtualInterface{}, err
	}
	ingress = uniqueACLIDs(ingress)
	egress = uniqueACLIDs(egress)
	for _, aclID := range append(append([]int64{}, ingress...), egress...) {
		if _, err := s.GetACL(ctx, aclID); err != nil {
			return VirtualInterface{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("begin interface ACL update: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM interface_acls WHERE interface_id = ?`, id); err != nil {
		return VirtualInterface{}, fmt.Errorf("clear interface ACLs: %w", err)
	}
	insert := func(direction string, aclIDs []int64) error {
		for position, aclID := range aclIDs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO interface_acls (interface_id, direction, acl_id, position)
				VALUES (?, ?, ?, ?)`, id, direction, aclID, position); err != nil {
				return fmt.Errorf("insert %s interface ACL: %w", direction, err)
			}
		}
		return nil
	}
	if err := insert("ingress", ingress); err != nil {
		return VirtualInterface{}, err
	}
	if err := insert("egress", egress); err != nil {
		return VirtualInterface{}, err
	}

	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		UPDATE virtual_interfaces SET ingress_acl_id = ?, egress_acl_id = ?, updated_at = ? WHERE id = ?`,
		firstACLID(ingress), firstACLID(egress), now, id,
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
	if err := tx.Commit(); err != nil {
		return VirtualInterface{}, fmt.Errorf("commit interface ACL update: %w", err)
	}
	return s.GetInterface(ctx, id)
}

func uniqueACLIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func firstACLID(ids []int64) *int64 {
	if len(ids) == 0 {
		return nil
	}
	id := ids[0]
	return &id
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
