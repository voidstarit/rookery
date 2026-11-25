package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrLightVMTemplateNotFound is returned when a template does not exist.
	ErrLightVMTemplateNotFound = errors.New("lightvm template not found")
	// ErrLightVMTemplateNameTaken is returned when a template name is in use.
	ErrLightVMTemplateNameTaken = errors.New("lightvm template name already exists")
	// ErrLightVMTemplateInUse is returned when a group pins a template version.
	ErrLightVMTemplateInUse = errors.New("lightvm template is used by a group")
	// ErrLightVMTemplateVersionNotFound is returned when a version does not exist.
	ErrLightVMTemplateVersionNotFound = errors.New("lightvm template version not found")
)

// LightVMTemplateDisk is one blank data disk in a template.
type LightVMTemplateDisk struct {
	StorageBackendID   int64  `json:"storage_backend_id"`
	SizeGB             int32  `json:"size_gb"`
	ThrottleClassID    *int64 `json:"throttle_class_id,omitempty"`
	Serial             string `json:"serial,omitempty"`
	StorageBackendName string `json:"-"`
	ThrottleClassName  string `json:"-"`
}

// LightVMTemplateInterface is one vhost-user interface in a template.
type LightVMTemplateInterface struct {
	NetworkID       int64    `json:"network_id"`
	IngressACLIDs   []int64  `json:"ingress_acl_ids,omitempty"`
	EgressACLIDs    []int64  `json:"egress_acl_ids,omitempty"`
	NetworkName     string   `json:"-"`
	IngressACLNames []string `json:"-"`
	EgressACLNames  []string `json:"-"`
}

// LightVMTemplateSpec is the stored specification of one immutable version.
type LightVMTemplateSpec struct {
	VCPUs            int                        `json:"vcpus"`
	MemoryMB         int                        `json:"memory_mb"`
	CgroupClass      string                     `json:"cgroup_class,omitempty"`
	KernelID         int64                      `json:"kernel_id"`
	InitramfsID      int64                      `json:"initramfs_id"`
	Image            string                     `json:"image"`
	Env              []LightVMEnvVar            `json:"env,omitempty"`
	ExecutablePath   string                     `json:"executable_path,omitempty"`
	ExecutableArgs   []string                   `json:"executable_args,omitempty"`
	ExecutableScript string                     `json:"executable_script,omitempty"`
	Append           string                     `json:"append,omitempty"`
	ConsoleEnabled   bool                       `json:"console_enabled"`
	Consoles         []LightVMConsole           `json:"consoles,omitempty"`
	VirtioFSShares   []LightVMVirtioFSShare     `json:"virtiofs_shares,omitempty"`
	Disks            []LightVMTemplateDisk      `json:"disks,omitempty"`
	Interfaces       []LightVMTemplateInterface `json:"interfaces,omitempty"`
}

// LightVMTemplate is the named template metadata.
type LightVMTemplate struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	LatestVersion   int32     `json:"latest_version"`
	LatestVersionID int64     `json:"latest_version_id"`
	VersionCount    int32     `json:"version_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// LightVMTemplateVersion is one immutable specification.
type LightVMTemplateVersion struct {
	ID         int64               `json:"id"`
	TemplateID int64               `json:"template_id"`
	Version    int32               `json:"version"`
	Spec       LightVMTemplateSpec `json:"spec"`
	CreatedAt  time.Time           `json:"created_at"`
}

// LightVMTemplateVersionSummary is a lightweight version row.
type LightVMTemplateVersionSummary struct {
	ID             int64     `json:"id"`
	TemplateID     int64     `json:"template_id"`
	Version        int32     `json:"version"`
	VCPUs          int32     `json:"vcpus"`
	MemoryMB       int32     `json:"memory_mb"`
	DiskCount      int32     `json:"disk_count"`
	InterfaceCount int32     `json:"interface_count"`
	CreatedAt      time.Time `json:"created_at"`
}

const lightVMTemplateSelect = `
	SELECT t.id, t.name, t.description,
	       COALESCE((SELECT version FROM lightvm_template_versions v
	                 WHERE v.template_id = t.id ORDER BY version DESC LIMIT 1), 0),
	       COALESCE((SELECT id FROM lightvm_template_versions v
	                 WHERE v.template_id = t.id ORDER BY version DESC LIMIT 1), 0),
	       (SELECT COUNT(*) FROM lightvm_template_versions v WHERE v.template_id = t.id),
	       t.created_at, t.updated_at
	FROM lightvm_templates AS t`

func normalizeLightVMTemplateSpec(spec LightVMTemplateSpec) LightVMTemplateSpec {
	if strings.TrimSpace(spec.Append) == "" {
		spec.Append = DefaultLightVMAppend
	}
	if len(spec.Consoles) == 0 {
		spec.Consoles = []LightVMConsole{{Name: "console"}}
	}
	if spec.Env == nil {
		spec.Env = []LightVMEnvVar{}
	}
	if spec.ExecutableArgs == nil {
		spec.ExecutableArgs = []string{}
	}
	if spec.VirtioFSShares == nil {
		spec.VirtioFSShares = []LightVMVirtioFSShare{}
	}
	if spec.Disks == nil {
		spec.Disks = []LightVMTemplateDisk{}
	}
	if spec.Interfaces == nil {
		spec.Interfaces = []LightVMTemplateInterface{}
	}
	return spec
}

// ListLightVMTemplates returns templates ordered by name.
func (s *Store) ListLightVMTemplates(ctx context.Context) ([]LightVMTemplate, error) {
	rows, err := s.db.QueryContext(ctx, lightVMTemplateSelect+` ORDER BY t.name COLLATE NOCASE, t.id`)
	if err != nil {
		return nil, fmt.Errorf("query lightvm templates: %w", err)
	}
	defer rows.Close()

	templates := make([]LightVMTemplate, 0)
	for rows.Next() {
		template, err := scanLightVMTemplate(rows)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lightvm templates: %w", err)
	}
	return templates, nil
}

// GetLightVMTemplate returns one template.
func (s *Store) GetLightVMTemplate(ctx context.Context, id int64) (LightVMTemplate, error) {
	row := s.db.QueryRowContext(ctx, lightVMTemplateSelect+` WHERE t.id = ?`, id)
	template, err := scanLightVMTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMTemplate{}, ErrLightVMTemplateNotFound
	}
	if err != nil {
		return LightVMTemplate{}, err
	}
	return template, nil
}

// CreateLightVMTemplate inserts a template and its first version.
func (s *Store) CreateLightVMTemplate(ctx context.Context, name, description string, spec LightVMTemplateSpec) (LightVMTemplate, LightVMTemplateVersion, error) {
	spec = normalizeLightVMTemplateSpec(spec)
	data, err := json.Marshal(spec)
	if err != nil {
		return LightVMTemplate{}, LightVMTemplateVersion{}, fmt.Errorf("encode lightvm template spec: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LightVMTemplate{}, LightVMTemplateVersion{}, fmt.Errorf("begin create lightvm template: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO lightvm_templates (name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?)`, name, description, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMTemplate{}, LightVMTemplateVersion{}, fmt.Errorf("%w: %q", ErrLightVMTemplateNameTaken, name)
		}
		return LightVMTemplate{}, LightVMTemplateVersion{}, fmt.Errorf("insert lightvm template: %w", err)
	}
	templateID, err := res.LastInsertId()
	if err != nil {
		return LightVMTemplate{}, LightVMTemplateVersion{}, fmt.Errorf("read lightvm template id: %w", err)
	}
	versionRes, err := tx.ExecContext(ctx, `
		INSERT INTO lightvm_template_versions (template_id, version, spec, created_at)
		VALUES (?, 1, ?, ?)`, templateID, string(data), now)
	if err != nil {
		return LightVMTemplate{}, LightVMTemplateVersion{}, fmt.Errorf("insert lightvm template version: %w", err)
	}
	versionID, err := versionRes.LastInsertId()
	if err != nil {
		return LightVMTemplate{}, LightVMTemplateVersion{}, fmt.Errorf("read lightvm template version id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LightVMTemplate{}, LightVMTemplateVersion{}, fmt.Errorf("commit lightvm template: %w", err)
	}
	template, err := s.GetLightVMTemplate(ctx, templateID)
	if err != nil {
		return LightVMTemplate{}, LightVMTemplateVersion{}, err
	}
	version, err := s.GetLightVMTemplateVersionByID(ctx, versionID)
	if err != nil {
		return LightVMTemplate{}, LightVMTemplateVersion{}, err
	}
	return template, version, nil
}

// UpdateLightVMTemplateMetadata updates only the name and description.
func (s *Store) UpdateLightVMTemplateMetadata(ctx context.Context, id int64, name, description string) (LightVMTemplate, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE lightvm_templates SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		name, description, time.Now().UTC().UnixMilli(), id)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMTemplate{}, fmt.Errorf("%w: %q", ErrLightVMTemplateNameTaken, name)
		}
		return LightVMTemplate{}, fmt.Errorf("update lightvm template: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVMTemplate{}, fmt.Errorf("read updated template rows: %w", err)
	}
	if affected == 0 {
		return LightVMTemplate{}, ErrLightVMTemplateNotFound
	}
	return s.GetLightVMTemplate(ctx, id)
}

// DeleteLightVMTemplate removes a template when no group pins a version.
func (s *Store) DeleteLightVMTemplate(ctx context.Context, id int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM lightvm_groups g
		JOIN lightvm_template_versions v ON v.id = g.template_version_id
		WHERE v.template_id = ?`, id).Scan(&count); err != nil {
		return fmt.Errorf("check lightvm template usage: %w", err)
	}
	if count > 0 {
		return ErrLightVMTemplateInUse
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM lightvm_templates WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrLightVMTemplateInUse
		}
		return fmt.Errorf("delete lightvm template: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted template rows: %w", err)
	}
	if affected == 0 {
		return ErrLightVMTemplateNotFound
	}
	return nil
}

// CreateLightVMTemplateVersion appends an immutable version.
func (s *Store) CreateLightVMTemplateVersion(ctx context.Context, templateID int64, spec LightVMTemplateSpec) (LightVMTemplateVersion, error) {
	if _, err := s.GetLightVMTemplate(ctx, templateID); err != nil {
		return LightVMTemplateVersion{}, err
	}
	spec = normalizeLightVMTemplateSpec(spec)
	data, err := json.Marshal(spec)
	if err != nil {
		return LightVMTemplateVersion{}, fmt.Errorf("encode lightvm template spec: %w", err)
	}
	var version int32
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM lightvm_template_versions WHERE template_id = ?`,
		templateID).Scan(&version); err != nil {
		return LightVMTemplateVersion{}, fmt.Errorf("next lightvm template version: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO lightvm_template_versions (template_id, version, spec, created_at)
		VALUES (?, ?, ?, ?)`, templateID, version, string(data), now)
	if err != nil {
		return LightVMTemplateVersion{}, fmt.Errorf("insert lightvm template version: %w", err)
	}
	versionID, err := res.LastInsertId()
	if err != nil {
		return LightVMTemplateVersion{}, fmt.Errorf("read lightvm template version id: %w", err)
	}
	return s.GetLightVMTemplateVersionByID(ctx, versionID)
}

// GetLightVMTemplateVersion returns a version that belongs to a template.
func (s *Store) GetLightVMTemplateVersion(ctx context.Context, templateID, versionID int64) (LightVMTemplateVersion, error) {
	version, err := s.GetLightVMTemplateVersionByID(ctx, versionID)
	if err != nil {
		return LightVMTemplateVersion{}, err
	}
	if version.TemplateID != templateID {
		return LightVMTemplateVersion{}, ErrLightVMTemplateVersionNotFound
	}
	return version, nil
}

// GetLightVMTemplateVersionByID returns a version by its global ID.
func (s *Store) GetLightVMTemplateVersionByID(ctx context.Context, versionID int64) (LightVMTemplateVersion, error) {
	var (
		version   LightVMTemplateVersion
		specJSON  string
		createdAt int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, template_id, version, spec, created_at
		FROM lightvm_template_versions WHERE id = ?`, versionID).Scan(
		&version.ID, &version.TemplateID, &version.Version, &specJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMTemplateVersion{}, ErrLightVMTemplateVersionNotFound
	}
	if err != nil {
		return LightVMTemplateVersion{}, fmt.Errorf("query lightvm template version: %w", err)
	}
	if err := json.Unmarshal([]byte(specJSON), &version.Spec); err != nil {
		return LightVMTemplateVersion{}, fmt.Errorf("decode lightvm template version: %w", err)
	}
	version.CreatedAt = time.UnixMilli(createdAt).UTC()
	return version, nil
}

// ListLightVMTemplateVersions returns version summaries ordered by version.
func (s *Store) ListLightVMTemplateVersions(ctx context.Context, templateID int64) ([]LightVMTemplateVersionSummary, error) {
	if _, err := s.GetLightVMTemplate(ctx, templateID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, template_id, version, spec, created_at
		FROM lightvm_template_versions WHERE template_id = ? ORDER BY version`, templateID)
	if err != nil {
		return nil, fmt.Errorf("query lightvm template versions: %w", err)
	}
	defer rows.Close()
	versions := make([]LightVMTemplateVersionSummary, 0)
	for rows.Next() {
		var (
			summary   LightVMTemplateVersionSummary
			specJSON  string
			createdAt int64
		)
		if err := rows.Scan(&summary.ID, &summary.TemplateID, &summary.Version, &specJSON, &createdAt); err != nil {
			return nil, err
		}
		var spec LightVMTemplateSpec
		if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
			return nil, fmt.Errorf("decode lightvm template version: %w", err)
		}
		summary.VCPUs = int32(spec.VCPUs)
		summary.MemoryMB = int32(spec.MemoryMB)
		summary.DiskCount = int32(len(spec.Disks))
		summary.InterfaceCount = int32(len(spec.Interfaces))
		summary.CreatedAt = time.UnixMilli(createdAt).UTC()
		versions = append(versions, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lightvm template versions: %w", err)
	}
	return versions, nil
}

func scanLightVMTemplate(s scanner) (LightVMTemplate, error) {
	var (
		template  LightVMTemplate
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(&template.ID, &template.Name, &template.Description,
		&template.LatestVersion, &template.LatestVersionID, &template.VersionCount,
		&createdAt, &updatedAt); err != nil {
		return LightVMTemplate{}, err
	}
	template.CreatedAt = time.UnixMilli(createdAt).UTC()
	template.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return template, nil
}
