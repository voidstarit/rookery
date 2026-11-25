package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrLightVMGroupNotFound is returned when a group does not exist.
	ErrLightVMGroupNotFound = errors.New("lightvm group not found")
	// ErrLightVMGroupNameTaken is returned when a group name is in use.
	ErrLightVMGroupNameTaken = errors.New("lightvm group name already exists")
	// ErrLightVMGroupNotEmpty is returned when a normal delete finds members.
	ErrLightVMGroupNotEmpty = errors.New("lightvm group still has members")
	// ErrLightVMGroupMemberNotFound is returned when a member does not exist.
	ErrLightVMGroupMemberNotFound = errors.New("lightvm group member not found")
	// ErrLightVMGroupMemberNameTaken is returned when a member name is in use.
	ErrLightVMGroupMemberNameTaken = errors.New("lightvm group member name already exists")
)

// LightVMGroup is the desired-state record for a managed fleet.
type LightVMGroup struct {
	ID                int64                `json:"id"`
	Name              string               `json:"name"`
	Description       string               `json:"description"`
	TemplateID        int64                `json:"template_id"`
	TemplateName      string               `json:"template_name"`
	TemplateVersionID int64                `json:"template_version_id"`
	TemplateVersion   int32                `json:"template_version"`
	DesiredCount      uint32               `json:"desired_count"`
	ActualCount       uint32               `json:"actual_count"`
	RunningCount      uint32               `json:"running_count"`
	ProvisioningCount uint32               `json:"provisioning_count"`
	RemovingCount     uint32               `json:"removing_count"`
	Drift             int32                `json:"drift"`
	Converged         bool                 `json:"converged"`
	Paused            bool                 `json:"paused"`
	ForceDelete       bool                 `json:"force_delete,omitempty"`
	State             string               `json:"state"`
	LastReconcileAt   time.Time            `json:"last_reconcile_at,omitempty"`
	LastError         string               `json:"last_error,omitempty"`
	RetryAttempts     int                  `json:"retry_attempts,omitempty"`
	NextRetryAt       time.Time            `json:"next_retry_at,omitempty"`
	Members           []LightVMGroupMember `json:"members,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

// LightVMGroupMember is one stable fleet member.
type LightVMGroupMember struct {
	ID                int64     `json:"id"`
	GroupID           int64     `json:"group_id"`
	Name              string    `json:"name"`
	TemplateVersionID int64     `json:"template_version_id"`
	TemplateVersion   int32     `json:"template_version"`
	State             string    `json:"state"`
	DesiredState      string    `json:"desired_state"`
	RetryAttempts     int       `json:"retry_attempts,omitempty"`
	RestartAttempts   int       `json:"restart_attempts,omitempty"`
	CleanupAttempts   int       `json:"cleanup_attempts,omitempty"`
	NextRetryAt       time.Time `json:"next_retry_at,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	WorkloadID        string    `json:"workload_id,omitempty"`
	Status            string    `json:"status,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// LightVMGroupUpdate is a partial group update.
type LightVMGroupUpdate struct {
	Name              *string
	Description       *string
	TemplateVersionID *int64
	DesiredCount      *uint32
	Paused            *bool
}

// LightVMGroupCounts are aggregate member counts for one group.
type LightVMGroupCounts struct {
	Actual       uint32
	Running      uint32
	Provisioning uint32
	Removing     uint32
	Converged    bool
}

const lightVMGroupSelect = `
	SELECT g.id, g.name, g.description, v.template_id, t.name,
	       g.template_version_id, v.version, g.desired_count, g.paused,
	       g.force_delete, g.state, g.last_reconcile_at, g.last_error,
	       g.retry_attempts, g.next_retry_at, g.created_at, g.updated_at
	FROM lightvm_groups AS g
	JOIN lightvm_template_versions AS v ON v.id = g.template_version_id
	JOIN lightvm_templates AS t ON t.id = v.template_id`

// ListLightVMGroups returns groups ordered by name.
func (s *Store) ListLightVMGroups(ctx context.Context) ([]LightVMGroup, error) {
	rows, err := s.db.QueryContext(ctx, lightVMGroupSelect+` ORDER BY g.name COLLATE NOCASE, g.id`)
	if err != nil {
		return nil, fmt.Errorf("query lightvm groups: %w", err)
	}
	defer rows.Close()
	groups := make([]LightVMGroup, 0)
	for rows.Next() {
		group, _, err := scanLightVMGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lightvm groups: %w", err)
	}
	counts, err := s.ListLightVMGroupCounts(ctx)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		applyLightVMGroupCounts(&groups[i], counts[groups[i].ID])
	}
	return groups, nil
}

// GetLightVMGroup returns one group with its members and counts.
func (s *Store) GetLightVMGroup(ctx context.Context, id int64) (LightVMGroup, error) {
	row := s.db.QueryRowContext(ctx, lightVMGroupSelect+` WHERE g.id = ?`, id)
	group, _, err := scanLightVMGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMGroup{}, ErrLightVMGroupNotFound
	}
	if err != nil {
		return LightVMGroup{}, err
	}
	counts, err := s.GetLightVMGroupCounts(ctx, id)
	if err != nil {
		return LightVMGroup{}, err
	}
	applyLightVMGroupCounts(&group, counts)
	members, err := s.ListGroupMembers(ctx, id)
	if err != nil {
		return LightVMGroup{}, err
	}
	group.Members = members
	return group, nil
}

// CreateLightVMGroup inserts a group pinned to an existing template version.
func (s *Store) CreateLightVMGroup(ctx context.Context, group LightVMGroup) (LightVMGroup, error) {
	if _, err := s.GetLightVMTemplateVersionByID(ctx, group.TemplateVersionID); err != nil {
		return LightVMGroup{}, err
	}
	now := time.Now().UTC().UnixMilli()
	state := group.State
	if state == "" {
		state = "active"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO lightvm_groups
			(name, description, template_version_id, desired_count, paused, force_delete,
			 state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		group.Name, group.Description, group.TemplateVersionID, group.DesiredCount,
		group.Paused, group.ForceDelete, state, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMGroup{}, fmt.Errorf("%w: %q", ErrLightVMGroupNameTaken, group.Name)
		}
		return LightVMGroup{}, fmt.Errorf("insert lightvm group: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return LightVMGroup{}, fmt.Errorf("read lightvm group id: %w", err)
	}
	return s.GetLightVMGroup(ctx, id)
}

// UpdateLightVMGroup applies a partial update. A new version must belong to the
// same template as the current pin.
func (s *Store) UpdateLightVMGroup(ctx context.Context, id int64, update LightVMGroupUpdate) (LightVMGroup, error) {
	current, err := s.GetLightVMGroup(ctx, id)
	if err != nil {
		return LightVMGroup{}, err
	}
	name := current.Name
	description := current.Description
	versionID := current.TemplateVersionID
	desired := current.DesiredCount
	paused := current.Paused
	if update.Name != nil {
		name = *update.Name
	}
	if update.Description != nil {
		description = *update.Description
	}
	if update.TemplateVersionID != nil {
		version, err := s.GetLightVMTemplateVersionByID(ctx, *update.TemplateVersionID)
		if err != nil {
			return LightVMGroup{}, err
		}
		if version.TemplateID != current.TemplateID {
			return LightVMGroup{}, ErrLightVMTemplateVersionNotFound
		}
		versionID = version.ID
	}
	if update.DesiredCount != nil {
		desired = *update.DesiredCount
	}
	if update.Paused != nil {
		paused = *update.Paused
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE lightvm_groups
		SET name = ?, description = ?, template_version_id = ?, desired_count = ?,
		    paused = ?, updated_at = ?
		WHERE id = ?`,
		name, description, versionID, desired, paused, time.Now().UTC().UnixMilli(), id)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMGroup{}, fmt.Errorf("%w: %q", ErrLightVMGroupNameTaken, name)
		}
		return LightVMGroup{}, fmt.Errorf("update lightvm group: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVMGroup{}, fmt.Errorf("read updated group rows: %w", err)
	}
	if affected == 0 {
		return LightVMGroup{}, ErrLightVMGroupNotFound
	}
	return s.GetLightVMGroup(ctx, id)
}

// SetLightVMGroupDeleting marks a non-empty group deleting; an empty group is
// removed and deleted=true is returned.
func (s *Store) SetLightVMGroupDeleting(ctx context.Context, id int64, force bool) (bool, LightVMGroup, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, LightVMGroup{}, fmt.Errorf("begin group delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM lightvm_group_members WHERE group_id = ?`, id).Scan(&count); err != nil {
		return false, LightVMGroup{}, fmt.Errorf("count group members: %w", err)
	}
	if count == 0 {
		res, err := tx.ExecContext(ctx, `DELETE FROM lightvm_groups WHERE id = ?`, id)
		if err != nil {
			return false, LightVMGroup{}, fmt.Errorf("delete empty group: %w", err)
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			return false, LightVMGroup{}, ErrLightVMGroupNotFound
		}
		if err := tx.Commit(); err != nil {
			return false, LightVMGroup{}, fmt.Errorf("commit empty group delete: %w", err)
		}
		return true, LightVMGroup{}, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE lightvm_groups
		SET state = 'deleting', force_delete = CASE WHEN ? THEN 1 ELSE force_delete END,
		    updated_at = ?
		WHERE id = ?`, force, time.Now().UTC().UnixMilli(), id); err != nil {
		return false, LightVMGroup{}, fmt.Errorf("mark group deleting: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, LightVMGroup{}, fmt.Errorf("commit group delete state: %w", err)
	}
	group, err := s.GetLightVMGroup(ctx, id)
	if err != nil {
		return false, LightVMGroup{}, err
	}
	return false, group, nil
}

// DeleteLightVMGroup removes an empty group.
func (s *Store) DeleteLightVMGroup(ctx context.Context, id int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lightvm_group_members WHERE group_id = ?`, id).Scan(&count); err != nil {
		return fmt.Errorf("count group members: %w", err)
	}
	if count > 0 {
		return ErrLightVMGroupNotEmpty
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM lightvm_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete lightvm group: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrLightVMGroupNotFound
	}
	return nil
}

// UpdateGroupReconcile records the result of a reconcile pass.
func (s *Store) UpdateGroupReconcile(ctx context.Context, id int64, lastAt time.Time, lastError string, retryAttempts int, nextRetryAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE lightvm_groups
		SET last_reconcile_at = ?, last_error = ?, retry_attempts = ?,
		    next_retry_at = ?, updated_at = ?
		WHERE id = ?`,
		lastAt.UTC().UnixMilli(), lastError, retryAttempts, nextRetryAt.UTC().UnixMilli(),
		time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("update group reconcile: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrLightVMGroupNotFound
	}
	return nil
}

// CreateGroupMemberIntent inserts a member only when the group is still
// active/unpaused and has room under the tracked count and expected version.
func (s *Store) CreateGroupMemberIntent(ctx context.Context, groupID int64, memberName string, expectedVersionID int64, version int32, desiredState string) (LightVMGroupMember, bool, error) {
	if desiredState != "running" && desiredState != "stopped" {
		return LightVMGroupMember{}, false, fmt.Errorf("invalid member desired state %q", desiredState)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LightVMGroupMember{}, false, fmt.Errorf("begin member intent: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var (
		state     string
		paused    int
		force     int
		versionID int64
		desired   uint32
	)
	err = tx.QueryRowContext(ctx, `
		SELECT state, paused, force_delete, template_version_id, desired_count
		FROM lightvm_groups WHERE id = ?`, groupID).Scan(&state, &paused, &force, &versionID, &desired)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMGroupMember{}, false, ErrLightVMGroupNotFound
	}
	if err != nil {
		return LightVMGroupMember{}, false, fmt.Errorf("read group intent: %w", err)
	}
	if state != "active" || paused != 0 || force != 0 || versionID != expectedVersionID {
		return LightVMGroupMember{}, false, nil
	}
	var tracked int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM lightvm_group_members WHERE group_id = ?`, groupID).Scan(&tracked); err != nil {
		return LightVMGroupMember{}, false, fmt.Errorf("count group members: %w", err)
	}
	if uint32(tracked) >= desired {
		return LightVMGroupMember{}, false, nil
	}
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO lightvm_group_members
			(group_id, member_name, template_version_id, template_version, state,
			 desired_state, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'provisioning', ?, ?, ?)`,
		groupID, memberName, versionID, version, desiredState, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMGroupMember{}, false, fmt.Errorf("%w: %q", ErrLightVMGroupMemberNameTaken, memberName)
		}
		return LightVMGroupMember{}, false, fmt.Errorf("insert group member: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return LightVMGroupMember{}, false, fmt.Errorf("read member id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LightVMGroupMember{}, false, fmt.Errorf("commit member intent: %w", err)
	}
	member, err := s.GetGroupMember(ctx, id)
	if err != nil {
		return LightVMGroupMember{}, false, err
	}
	return member, true, nil
}

// GetGroupMember returns one member with its derived workload fields.
func (s *Store) GetGroupMember(ctx context.Context, id int64) (LightVMGroupMember, error) {
	row := s.db.QueryRowContext(ctx, lightVMGroupMemberSelect+` WHERE m.id = ?`, id)
	member, err := scanLightVMGroupMember(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMGroupMember{}, ErrLightVMGroupMemberNotFound
	}
	if err != nil {
		return LightVMGroupMember{}, err
	}
	return member, nil
}

// ListGroupMembers returns members ordered by name.
func (s *Store) ListGroupMembers(ctx context.Context, groupID int64) ([]LightVMGroupMember, error) {
	rows, err := s.db.QueryContext(ctx, lightVMGroupMemberSelect+`
		WHERE m.group_id = ? ORDER BY m.member_name COLLATE NOCASE, m.id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("query group members: %w", err)
	}
	defer rows.Close()
	members := make([]LightVMGroupMember, 0)
	for rows.Next() {
		member, err := scanLightVMGroupMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group members: %w", err)
	}
	return members, nil
}

// SetGroupMemberState updates only the lifecycle state.
func (s *Store) SetGroupMemberState(ctx context.Context, id int64, state string) error {
	if state != "provisioning" && state != "active" && state != "removing" {
		return fmt.Errorf("invalid group member state %q", state)
	}
	return s.updateGroupMember(ctx, id, `state = ?`, state)
}

// SetGroupMemberDesiredState updates only the power intent.
func (s *Store) SetGroupMemberDesiredState(ctx context.Context, id int64, desiredState string) error {
	if desiredState != "running" && desiredState != "stopped" {
		return fmt.Errorf("invalid member desired state %q", desiredState)
	}
	return s.updateGroupMember(ctx, id, `desired_state = ?`, desiredState)
}

// SetGroupMemberPower updates member intent and the LightVM mirror in one
// transaction. A non-empty lightVMID also bumps lifecycle_generation.
func (s *Store) SetGroupMemberPower(ctx context.Context, memberID int64, lightVMID, desiredState string) error {
	if desiredState != "running" && desiredState != "stopped" {
		return fmt.Errorf("invalid member desired state %q", desiredState)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin member power: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `UPDATE lightvm_group_members SET desired_state = ?, updated_at = ? WHERE id = ?`, desiredState, now, memberID)
	if err != nil {
		return fmt.Errorf("update member desired state: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrLightVMGroupMemberNotFound
	}
	if lightVMID != "" {
		res, err = tx.ExecContext(ctx, `
			UPDATE lightvms
			SET desired_state = ?, lifecycle_generation = lifecycle_generation + 1, updated_at = ?
			WHERE id = ?`, desiredState, now, lightVMID)
		if err != nil {
			return fmt.Errorf("update lightvm desired state: %w", err)
		}
		affected, _ = res.RowsAffected()
		if affected == 0 {
			return ErrLightVMNotFound
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit member power: %w", err)
	}
	return nil
}

// SetGroupMemberRetry updates create/repair backoff.
func (s *Store) SetGroupMemberRetry(ctx context.Context, id int64, attempts int, nextRetryAt time.Time, lastError string) error {
	return s.updateGroupMember(ctx, id, `retry_attempts = ?, next_retry_at = ?, last_error = ?`,
		attempts, nextRetryAt.UTC().UnixMilli(), lastError)
}

// SetGroupMemberRestart updates start backoff.
func (s *Store) SetGroupMemberRestart(ctx context.Context, id int64, attempts int, nextRetryAt time.Time, lastError string) error {
	return s.updateGroupMember(ctx, id, `restart_attempts = ?, next_retry_at = ?, last_error = ?`,
		attempts, nextRetryAt.UTC().UnixMilli(), lastError)
}

// SetGroupMemberCleanup updates cleanup backoff.
func (s *Store) SetGroupMemberCleanup(ctx context.Context, id int64, attempts int, nextRetryAt time.Time, lastError string) error {
	return s.updateGroupMember(ctx, id, `cleanup_attempts = ?, next_retry_at = ?, last_error = ?`,
		attempts, nextRetryAt.UTC().UnixMilli(), lastError)
}

func (s *Store) updateGroupMember(ctx context.Context, id int64, set string, args ...any) error {
	args = append(args, time.Now().UTC().UnixMilli(), id)
	res, err := s.db.ExecContext(ctx, `UPDATE lightvm_group_members SET `+set+`, updated_at = ? WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("update group member: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrLightVMGroupMemberNotFound
	}
	return nil
}

// DeleteGroupMember removes a member row.
func (s *Store) DeleteGroupMember(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM lightvm_group_members WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete group member: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrLightVMGroupMemberNotFound
	}
	return nil
}

// GetLightVMByGroupMemberID returns the member's LightVM.
func (s *Store) GetLightVMByGroupMemberID(ctx context.Context, memberID int64) (LightVM, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM lightvms WHERE group_member_id = ?`, memberID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVM{}, ErrLightVMNotFound
	}
	if err != nil {
		return LightVM{}, fmt.Errorf("query member lightvm: %w", err)
	}
	return s.GetLightVM(ctx, id)
}

// GetGroupMemberByLightVMID returns the member that owns a LightVM.
func (s *Store) GetGroupMemberByLightVMID(ctx context.Context, lightVMID string) (LightVMGroupMember, error) {
	var memberID int64
	err := s.db.QueryRowContext(ctx, `SELECT group_member_id FROM lightvms WHERE id = ? AND group_member_id IS NOT NULL`, lightVMID).Scan(&memberID)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMGroupMember{}, ErrLightVMGroupMemberNotFound
	}
	if err != nil {
		return LightVMGroupMember{}, fmt.Errorf("query lightvm member: %w", err)
	}
	return s.GetGroupMember(ctx, memberID)
}

// ListDisksByGroupMemberID returns owned disks.
func (s *Store) ListDisksByGroupMemberID(ctx context.Context, memberID int64) ([]Disk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM disks WHERE group_member_id = ? ORDER BY id`, memberID)
	if err != nil {
		return nil, fmt.Errorf("query member disks: %w", err)
	}
	defer rows.Close()
	disks := make([]Disk, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		disk, err := s.GetDisk(ctx, id)
		if err != nil {
			return nil, err
		}
		disks = append(disks, disk)
	}
	return disks, rows.Err()
}

// ListInterfacesByGroupMemberID returns owned interfaces.
func (s *Store) ListInterfacesByGroupMemberID(ctx context.Context, memberID int64) ([]VirtualInterface, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM virtual_interfaces WHERE group_member_id = ? ORDER BY id`, memberID)
	if err != nil {
		return nil, fmt.Errorf("query member interfaces: %w", err)
	}
	defer rows.Close()
	interfaces := make([]VirtualInterface, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		iface, err := s.GetInterface(ctx, id)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, iface)
	}
	return interfaces, rows.Err()
}

// SetDiskGroupMember assigns or clears a disk's owning group member.
func (s *Store) SetDiskGroupMember(ctx context.Context, diskID int64, memberID *int64) error {
	var value any
	if memberID != nil {
		value = *memberID
	}
	res, err := s.db.ExecContext(ctx, `UPDATE disks SET group_member_id = ? WHERE id = ?`, value, diskID)
	if err != nil {
		return fmt.Errorf("set disk group member: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrDiskNotFound
	}
	return nil
}

// SetInterfaceGroupMember assigns or clears an interface's owning group member.
func (s *Store) SetInterfaceGroupMember(ctx context.Context, interfaceID int64, memberID *int64) error {
	var value any
	if memberID != nil {
		value = *memberID
	}
	res, err := s.db.ExecContext(ctx, `UPDATE virtual_interfaces SET group_member_id = ? WHERE id = ?`, value, interfaceID)
	if err != nil {
		return fmt.Errorf("set interface group member: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrInterfaceNotFound
	}
	return nil
}

// ClearLightVMGroupOwner makes a LightVM unmanaged.
func (s *Store) ClearLightVMGroupOwner(ctx context.Context, lightVMID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE lightvms SET group_member_id = NULL, updated_at = ? WHERE id = ?`,
		time.Now().UTC().UnixMilli(), lightVMID)
	if err != nil {
		return fmt.Errorf("clear lightvm owner: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrLightVMNotFound
	}
	return nil
}

// CountTrackedGroupMembers returns all member rows for a group.
func (s *Store) CountTrackedGroupMembers(ctx context.Context, groupID int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lightvm_group_members WHERE group_id = ?`, groupID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count tracked members: %w", err)
	}
	return count, nil
}

// GetLightVMGroupCounts returns aggregate counts for one group.
func (s *Store) GetLightVMGroupCounts(ctx context.Context, groupID int64) (LightVMGroupCounts, error) {
	var (
		counts  LightVMGroupCounts
		desired int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT g.desired_count,
		       COALESCE(SUM(CASE WHEN lv.id IS NOT NULL THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN lv.status = 'running' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN m.state = 'provisioning' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN m.state = 'removing' THEN 1 ELSE 0 END), 0)
		FROM lightvm_groups g
		LEFT JOIN lightvm_group_members m ON m.group_id = g.id
		LEFT JOIN lightvms lv ON lv.group_member_id = m.id
		WHERE g.id = ? GROUP BY g.id`, groupID).Scan(
		&desired, &counts.Actual, &counts.Running, &counts.Provisioning, &counts.Removing)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMGroupCounts{}, ErrLightVMGroupNotFound
	}
	if err != nil {
		return LightVMGroupCounts{}, fmt.Errorf("query group counts: %w", err)
	}
	counts.Converged = int(counts.Actual) == desired && counts.Provisioning == 0 && counts.Removing == 0
	return counts, nil
}

// ListLightVMGroupCounts returns aggregate counts for every group.
func (s *Store) ListLightVMGroupCounts(ctx context.Context) (map[int64]LightVMGroupCounts, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.desired_count,
		       COALESCE(SUM(CASE WHEN lv.id IS NOT NULL THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN lv.status = 'running' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN m.state = 'provisioning' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN m.state = 'removing' THEN 1 ELSE 0 END), 0)
		FROM lightvm_groups g
		LEFT JOIN lightvm_group_members m ON m.group_id = g.id
		LEFT JOIN lightvms lv ON lv.group_member_id = m.id
		GROUP BY g.id`)
	if err != nil {
		return nil, fmt.Errorf("query group counts: %w", err)
	}
	defer rows.Close()
	result := make(map[int64]LightVMGroupCounts)
	for rows.Next() {
		var (
			id      int64
			desired int
			counts  LightVMGroupCounts
		)
		if err := rows.Scan(&id, &desired, &counts.Actual, &counts.Running, &counts.Provisioning, &counts.Removing); err != nil {
			return nil, err
		}
		counts.Converged = int(counts.Actual) == desired && counts.Provisioning == 0 && counts.Removing == 0
		result[id] = counts
	}
	return result, rows.Err()
}

func applyLightVMGroupCounts(group *LightVMGroup, counts LightVMGroupCounts) {
	group.ActualCount = counts.Actual
	group.RunningCount = counts.Running
	group.ProvisioningCount = counts.Provisioning
	group.RemovingCount = counts.Removing
	group.Converged = counts.Converged
	group.Drift = int32(group.DesiredCount) - int32(group.ActualCount)
}

func scanLightVMGroup(s scanner) (LightVMGroup, int64, error) {
	var (
		group     LightVMGroup
		paused    int
		force     int
		lastAt    int64
		nextAt    int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(&group.ID, &group.Name, &group.Description, &group.TemplateID,
		&group.TemplateName, &group.TemplateVersionID, &group.TemplateVersion,
		&group.DesiredCount, &paused, &force, &group.State, &lastAt,
		&group.LastError, &group.RetryAttempts, &nextAt, &createdAt, &updatedAt); err != nil {
		return LightVMGroup{}, 0, err
	}
	group.Paused = paused != 0
	group.ForceDelete = force != 0
	if lastAt > 0 {
		group.LastReconcileAt = time.UnixMilli(lastAt).UTC()
	}
	if nextAt > 0 {
		group.NextRetryAt = time.UnixMilli(nextAt).UTC()
	}
	group.CreatedAt = time.UnixMilli(createdAt).UTC()
	group.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return group, group.TemplateVersionID, nil
}

const lightVMGroupMemberSelect = `
	SELECT m.id, m.group_id, m.member_name, m.template_version_id,
	       m.template_version, m.state, m.desired_state, m.retry_attempts,
	       m.restart_attempts, m.cleanup_attempts, m.next_retry_at, m.last_error,
	       m.created_at, m.updated_at, lv.id, lv.status
	FROM lightvm_group_members AS m
	LEFT JOIN lightvms AS lv ON lv.group_member_id = m.id`

func scanLightVMGroupMember(s scanner) (LightVMGroupMember, error) {
	var (
		member    LightVMGroupMember
		nextAt    int64
		createdAt int64
		updatedAt int64
		workload  sql.NullString
		status    sql.NullString
	)
	if err := s.Scan(&member.ID, &member.GroupID, &member.Name, &member.TemplateVersionID,
		&member.TemplateVersion, &member.State, &member.DesiredState, &member.RetryAttempts,
		&member.RestartAttempts, &member.CleanupAttempts, &nextAt, &member.LastError,
		&createdAt, &updatedAt, &workload, &status); err != nil {
		return LightVMGroupMember{}, err
	}
	if workload.Valid {
		member.WorkloadID = workload.String
	}
	if status.Valid {
		member.Status = status.String
	} else {
		member.Status = "missing"
	}
	if nextAt > 0 {
		member.NextRetryAt = time.UnixMilli(nextAt).UTC()
	}
	member.CreatedAt = time.UnixMilli(createdAt).UTC()
	member.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return member, nil
}
