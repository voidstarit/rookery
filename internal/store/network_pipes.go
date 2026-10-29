package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNetworkPipeNotFound = errors.New("network pipe not found")
	ErrNetworkPipeExists   = errors.New("network pipe already exists")
)

// NetworkPipe is a VPP pipe pair connecting two bridge domains.
type NetworkPipe struct {
	ID                 int64     `json:"id"`
	NetworkAID         int64     `json:"network_a_id"`
	NetworkAName       string    `json:"network_a_name"`
	NetworkBID         int64     `json:"network_b_id"`
	NetworkBName       string    `json:"network_b_name"`
	Instance           uint32    `json:"instance"`
	ParentSwIfIndex    uint32    `json:"parent_sw_if_index"`
	EndpointASwIfIndex uint32    `json:"endpoint_a_sw_if_index"`
	EndpointBSwIfIndex uint32    `json:"endpoint_b_sw_if_index"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

const networkPipeSelect = `
	SELECT p.id, p.network_a_id, a.name, p.network_b_id, b.name,
	       p.instance, p.parent_sw_if_index, p.endpoint_a_sw_if_index,
	       p.endpoint_b_sw_if_index, p.created_at, p.updated_at
	FROM network_pipes AS p
	JOIN networks AS a ON a.id = p.network_a_id
	JOIN networks AS b ON b.id = p.network_b_id`

func (s *Store) ListNetworkPipes(ctx context.Context) ([]NetworkPipe, error) {
	rows, err := s.db.QueryContext(ctx, networkPipeSelect+` ORDER BY p.id`)
	if err != nil {
		return nil, fmt.Errorf("query network pipes: %w", err)
	}
	defer rows.Close()
	pipes := make([]NetworkPipe, 0)
	for rows.Next() {
		pipe, err := scanNetworkPipe(rows)
		if err != nil {
			return nil, err
		}
		pipes = append(pipes, pipe)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate network pipes: %w", err)
	}
	return pipes, nil
}

func (s *Store) GetNetworkPipe(ctx context.Context, id int64) (NetworkPipe, error) {
	row := s.db.QueryRowContext(ctx, networkPipeSelect+` WHERE p.id = ?`, id)
	pipe, err := scanNetworkPipe(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NetworkPipe{}, ErrNetworkPipeNotFound
	}
	if err != nil {
		return NetworkPipe{}, err
	}
	return pipe, nil
}

func (s *Store) CreateNetworkPipe(ctx context.Context, pipe NetworkPipe) (NetworkPipe, error) {
	if pipe.NetworkAID == pipe.NetworkBID {
		return NetworkPipe{}, errors.New("a pipe requires two different networks")
	}
	if pipe.NetworkAID > pipe.NetworkBID {
		pipe.NetworkAID, pipe.NetworkBID = pipe.NetworkBID, pipe.NetworkAID
	}
	if _, err := s.GetNetwork(ctx, pipe.NetworkAID); err != nil {
		return NetworkPipe{}, err
	}
	if _, err := s.GetNetwork(ctx, pipe.NetworkBID); err != nil {
		return NetworkPipe{}, err
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO network_pipes
			(network_a_id, network_b_id, instance, parent_sw_if_index,
			 endpoint_a_sw_if_index, endpoint_b_sw_if_index, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		pipe.NetworkAID, pipe.NetworkBID, pipe.Instance, pipe.ParentSwIfIndex,
		pipe.EndpointASwIfIndex, pipe.EndpointBSwIfIndex, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return NetworkPipe{}, ErrNetworkPipeExists
		}
		return NetworkPipe{}, fmt.Errorf("insert network pipe: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return NetworkPipe{}, fmt.Errorf("read inserted network pipe id: %w", err)
	}
	return s.GetNetworkPipe(ctx, id)
}

func (s *Store) SetNetworkPipeRuntime(ctx context.Context, id int64, instance, parent, endpointA, endpointB uint32) (NetworkPipe, error) {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE network_pipes SET instance = ?, parent_sw_if_index = ?,
			endpoint_a_sw_if_index = ?, endpoint_b_sw_if_index = ?, updated_at = ?
		WHERE id = ?`,
		instance, parent, endpointA, endpointB, time.Now().UTC().UnixMilli(), id,
	); err != nil {
		return NetworkPipe{}, fmt.Errorf("update network pipe runtime: %w", err)
	}
	return s.GetNetworkPipe(ctx, id)
}

func (s *Store) DeleteNetworkPipe(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin network pipe delete: %w", err)
	}
	defer tx.Rollback()

	var instance int64
	err = tx.QueryRowContext(ctx, `SELECT instance FROM network_pipes WHERE id = ?`, id).Scan(&instance)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNetworkPipeNotFound
	}
	if err != nil {
		return fmt.Errorf("read network pipe instance: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM network_pipes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete network pipe: %w", err)
	}
	for _, name := range NetworkPipeEndpointNames(uint32(instance)) {
		for _, statement := range []string{
			`DELETE FROM vpp_interface_addresses WHERE interface_name = ?`,
			`DELETE FROM vpp_interface_dhcp_clients WHERE interface_name = ?`,
			`DELETE FROM vpp_interface_route_tables WHERE interface_name = ?`,
		} {
			if _, err := tx.ExecContext(ctx, statement, name); err != nil {
				return fmt.Errorf("clean up pipe endpoint %s: %w", name, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit network pipe delete: %w", err)
	}
	return nil
}

func (s *Store) CountNetworkPipesForNetwork(ctx context.Context, networkID int64) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM network_pipes
		WHERE network_a_id = ? OR network_b_id = ?`, networkID, networkID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count network pipes: %w", err)
	}
	return count, nil
}

// NetworkPipeEndpointNames returns the VPP interface names for the two ends
// of a pipe instance.
func NetworkPipeEndpointNames(instance uint32) [2]string {
	return [2]string{
		fmt.Sprintf("pipe%d.0", instance),
		fmt.Sprintf("pipe%d.1", instance),
	}
}

func scanNetworkPipe(s scanner) (NetworkPipe, error) {
	var (
		pipe      NetworkPipe
		instance  int64
		parent    int64
		endpointA int64
		endpointB int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(&pipe.ID, &pipe.NetworkAID, &pipe.NetworkAName,
		&pipe.NetworkBID, &pipe.NetworkBName, &instance, &parent,
		&endpointA, &endpointB, &createdAt, &updatedAt); err != nil {
		return NetworkPipe{}, err
	}
	pipe.Instance = uint32(instance)
	pipe.ParentSwIfIndex = uint32(parent)
	pipe.EndpointASwIfIndex = uint32(endpointA)
	pipe.EndpointBSwIfIndex = uint32(endpointB)
	pipe.CreatedAt = time.UnixMilli(createdAt).UTC()
	pipe.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return pipe, nil
}
