package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/voidstarit/rookery/internal/labels"
)

// EntityLabels is the set of labels attached to one entity.
type EntityLabels struct {
	EntityType string   `json:"entity_type"`
	EntityID   string   `json:"entity_id"`
	Labels     []string `json:"labels"`
}

// ListEntityLabels returns labels for every entity of a type.
func (s *Store) ListEntityLabels(ctx context.Context, entityType string) ([]EntityLabels, error) {
	entityType, err := validateEntityType(entityType)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT entity_id, label FROM entity_labels
		WHERE entity_type = ?
		ORDER BY entity_id, label`, entityType)
	if err != nil {
		return nil, fmt.Errorf("list entity labels: %w", err)
	}
	defer rows.Close()

	result := make([]EntityLabels, 0)
	index := make(map[string]int)
	for rows.Next() {
		var entityID, label string
		if err := rows.Scan(&entityID, &label); err != nil {
			return nil, fmt.Errorf("scan entity label: %w", err)
		}
		position, ok := index[entityID]
		if !ok {
			position = len(result)
			index[entityID] = position
			result = append(result, EntityLabels{EntityType: entityType, EntityID: entityID})
		}
		result[position].Labels = append(result[position].Labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entity labels: %w", err)
	}
	return result, nil
}

// GetEntityLabels returns labels for one entity. Missing labels are not an
// error; the returned Labels slice is empty.
func (s *Store) GetEntityLabels(ctx context.Context, entityType, entityID string) (EntityLabels, error) {
	entityType, entityID, err := validateEntitySelector(entityType, entityID)
	if err != nil {
		return EntityLabels{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT label FROM entity_labels
		WHERE entity_type = ? AND entity_id = ?
		ORDER BY label`, entityType, entityID)
	if err != nil {
		return EntityLabels{}, fmt.Errorf("get entity labels: %w", err)
	}
	defer rows.Close()
	result := EntityLabels{EntityType: entityType, EntityID: entityID, Labels: make([]string, 0)}
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return EntityLabels{}, fmt.Errorf("scan entity label: %w", err)
		}
		result.Labels = append(result.Labels, label)
	}
	if err := rows.Err(); err != nil {
		return EntityLabels{}, fmt.Errorf("iterate entity labels: %w", err)
	}
	return result, nil
}

// SetEntityLabels replaces every label on an entity.
func (s *Store) SetEntityLabels(ctx context.Context, entityType, entityID string, values []string) (EntityLabels, error) {
	entityType, entityID, err := validateEntitySelector(entityType, entityID)
	if err != nil {
		return EntityLabels{}, err
	}
	normalized, err := labels.Normalize(values)
	if err != nil {
		return EntityLabels{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EntityLabels{}, fmt.Errorf("begin entity labels transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM entity_labels WHERE entity_type = ? AND entity_id = ?`,
		entityType, entityID); err != nil {
		return EntityLabels{}, fmt.Errorf("clear entity labels: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	for _, label := range normalized {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO entity_labels (entity_type, entity_id, label, created_at)
			VALUES (?, ?, ?, ?)`, entityType, entityID, label, now); err != nil {
			return EntityLabels{}, fmt.Errorf("insert entity label: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return EntityLabels{}, fmt.Errorf("commit entity labels: %w", err)
	}
	return EntityLabels{EntityType: entityType, EntityID: entityID, Labels: normalized}, nil
}

// DeleteEntityLabels removes every label from one entity.
func (s *Store) DeleteEntityLabels(ctx context.Context, entityType, entityID string) error {
	entityType, entityID, err := validateEntitySelector(entityType, entityID)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM entity_labels WHERE entity_type = ? AND entity_id = ?`,
		entityType, entityID); err != nil {
		return fmt.Errorf("delete entity labels: %w", err)
	}
	return nil
}

func validateEntityType(entityType string) (string, error) {
	entityType = strings.TrimSpace(entityType)
	if !labels.ValidEntityType(entityType) {
		return "", fmt.Errorf("unknown entity type %q", entityType)
	}
	return entityType, nil
}

func validateEntitySelector(entityType, entityID string) (string, string, error) {
	entityType, err := validateEntityType(entityType)
	if err != nil {
		return "", "", err
	}
	entityID = strings.TrimSpace(entityID)
	if entityID == "" {
		return "", "", fmt.Errorf("entity id is required")
	}
	if len(entityID) > 200 {
		return "", "", fmt.Errorf("entity id is too long")
	}
	return entityType, entityID, nil
}
