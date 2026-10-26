package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/store"
)

type diskScriptInfo struct {
	Operation string            `json:"operation"`
	RequestID string            `json:"request_id"`
	Disk      diskScriptDisk    `json:"disk"`
	Template  *diskScriptDisk   `json:"template,omitempty"`
	Backend   diskScriptBackend `json:"backend"`
}

type diskScriptDisk struct {
	ID               *int64 `json:"id,omitempty"`
	Name             string `json:"name"`
	SizeGB           int    `json:"size_gb"`
	VG               string `json:"vg"`
	ThinPool         string `json:"thin_pool"`
	Device           string `json:"device"`
	IsTemplate       bool   `json:"is_template"`
	TemplateDiskID   *int64 `json:"template_disk_id,omitempty"`
	TemplateDiskName string `json:"template_disk_name,omitempty"`
}

type diskProvisionInfo struct {
	Operation  string            `json:"operation"`
	RequestID  string            `json:"request_id"`
	Disk       diskScriptDisk    `json:"disk"`
	Template   diskScriptDisk    `json:"template"`
	Backend    diskScriptBackend `json:"backend"`
	Properties map[string]any    `json:"properties"`
}

type diskScriptBackend struct {
	ID     int64                      `json:"id"`
	Name   string                     `json:"name"`
	Driver string                     `json:"driver"`
	Config store.StorageBackendConfig `json:"config"`
}

func diskScriptRecord(disk store.Disk) diskScriptDisk {
	return diskScriptDisk{
		ID: &disk.ID, Name: disk.Name, SizeGB: disk.SizeGB,
		VG: disk.VG, ThinPool: disk.ThinPool, Device: disk.Device,
		IsTemplate: disk.IsTemplate, TemplateDiskID: disk.TemplateDiskID, TemplateDiskName: disk.TemplateDiskName,
	}
}

func (s *diskService) runTemplateProvisioningScript(ctx context.Context, backend store.StorageBackend, disk store.Disk, template *store.Disk, properties map[string]any) error {
	if template == nil {
		return nil
	}
	script := strings.TrimSpace(template.ProvisioningScript)
	if script == "" {
		if len(properties) > 0 {
			return fmt.Errorf("template %q has no provisioning_script", template.Name)
		}
		return nil
	}
	timeout := 5 * time.Minute
	if configured := strings.TrimSpace(template.ProvisioningTimeout); configured != "" {
		parsed, err := time.ParseDuration(configured)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("invalid provisioning_timeout %q", configured)
		}
		timeout = parsed
	}
	if properties == nil {
		properties = map[string]any{}
	}
	info := diskProvisionInfo{
		Operation:  "provision",
		RequestID:  uuid.NewString(),
		Disk:       diskScriptRecord(disk),
		Template:   diskScriptRecord(*template),
		Backend:    diskScriptBackend{ID: backend.ID, Name: backend.Name, Driver: backend.Driver, Config: backend.Config},
		Properties: properties,
	}
	payload, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("encode provisioning payload: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, script)
	command.Env = append(os.Environ(), "ROOKERY_PROVISION_JSON="+string(payload))
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err = command.Run()
	text := strings.TrimSpace(output.String())
	if err != nil {
		s.logger.Error().
			Str("script", script).
			Str("operation", "provision").
			Str("disk", disk.Name).
			Err(err).
			Str("output", text).
			Msg("template provisioning script failed")
		if text == "" {
			return fmt.Errorf("template provisioning script failed: %w", err)
		}
		return fmt.Errorf("template provisioning script failed: %w: %s", err, text)
	}
	s.logger.Info().
		Str("script", script).
		Str("operation", "provision").
		Str("disk", disk.Name).
		Str("output", text).
		Msg("template provisioning script completed")
	return nil
}

func (s *diskService) runDiskPreparationScript(ctx context.Context, operation string, backend store.StorageBackend, disk store.Disk, template *store.Disk) error {
	script := strings.TrimSpace(backend.Config.PreparationScript)
	if script == "" {
		return fmt.Errorf("backend %q has no preparation_script", backend.Name)
	}
	timeout := 5 * time.Minute
	if configured := strings.TrimSpace(backend.Config.PreparationTimeout); configured != "" {
		parsed, err := time.ParseDuration(configured)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("invalid preparation_timeout %q", configured)
		}
		timeout = parsed
	}
	info := diskScriptInfo{
		Operation: operation,
		RequestID: uuid.NewString(),
		Disk:      diskScriptRecord(disk),
		Backend:   diskScriptBackend{ID: backend.ID, Name: backend.Name, Driver: backend.Driver, Config: backend.Config},
	}
	if template != nil {
		value := diskScriptRecord(*template)
		info.Template = &value
	}
	payload, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("encode disk preparation payload: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, script)
	command.Env = append(os.Environ(), "ROOKERY_DISK_JSON="+string(payload))
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err = command.Run()
	text := strings.TrimSpace(output.String())
	if err != nil {
		s.logger.Error().
			Str("script", script).
			Str("operation", operation).
			Str("disk", disk.Name).
			Err(err).
			Str("output", text).
			Msg("disk preparation script failed")
		if text == "" {
			return fmt.Errorf("disk preparation script failed: %w", err)
		}
		return fmt.Errorf("disk preparation script failed: %w: %s", err, text)
	}
	s.logger.Info().
		Str("script", script).
		Str("operation", operation).
		Str("disk", disk.Name).
		Str("output", text).
		Msg("disk preparation script completed")
	return nil
}
