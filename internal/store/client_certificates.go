package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ClientCertificate is the metadata for an issued mTLS client certificate.
// Private keys are never stored; they are returned once at issuance.
type ClientCertificate struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Serial    string    `json:"serial"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateClientCertificate records an issued client certificate.
func (s *Store) CreateClientCertificate(ctx context.Context, cert ClientCertificate) (ClientCertificate, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO client_certificates (name, serial, not_before, not_after, revoked, created_at)
		VALUES (?, ?, ?, ?, 0, ?)`,
		cert.Name, cert.Serial, cert.NotBefore.UTC().UnixMilli(), cert.NotAfter.UTC().UnixMilli(), now,
	)
	if err != nil {
		return ClientCertificate{}, fmt.Errorf("insert client certificate: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ClientCertificate{}, fmt.Errorf("read client certificate id: %w", err)
	}
	return s.GetClientCertificate(ctx, id)
}

// GetClientCertificate returns one issued client certificate by record id.
func (s *Store) GetClientCertificate(ctx context.Context, id int64) (ClientCertificate, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, serial, not_before, not_after, revoked, created_at
		FROM client_certificates WHERE id = ?`, id)
	cert, err := scanClientCertificate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ClientCertificate{}, ErrNotFound
	}
	if err != nil {
		return ClientCertificate{}, err
	}
	return cert, nil
}

// ListClientCertificates returns issued client certificates, newest first.
func (s *Store) ListClientCertificates(ctx context.Context) ([]ClientCertificate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, serial, not_before, not_after, revoked, created_at
		FROM client_certificates ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query client certificates: %w", err)
	}
	defer rows.Close()
	certs := make([]ClientCertificate, 0)
	for rows.Next() {
		cert, err := scanClientCertificate(rows)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate client certificates: %w", err)
	}
	return certs, nil
}

// RevokeClientCertificate marks a client certificate revoked.
func (s *Store) RevokeClientCertificate(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE client_certificates SET revoked = 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("revoke client certificate: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// IsClientCertificateRevoked reports whether a serial is revoked. Unknown
// serials are not considered revoked.
func (s *Store) IsClientCertificateRevoked(ctx context.Context, serial string) (bool, error) {
	var revoked int64
	err := s.db.QueryRowContext(ctx,
		`SELECT revoked FROM client_certificates WHERE serial = ?`, serial,
	).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read client certificate revocation: %w", err)
	}
	return revoked != 0, nil
}

func scanClientCertificate(s scanner) (ClientCertificate, error) {
	var (
		cert      ClientCertificate
		revoked   int64
		notBefore int64
		notAfter  int64
		createdAt int64
	)
	if err := s.Scan(&cert.ID, &cert.Name, &cert.Serial, &notBefore, &notAfter, &revoked, &createdAt); err != nil {
		return ClientCertificate{}, err
	}
	cert.NotBefore = time.UnixMilli(notBefore).UTC()
	cert.NotAfter = time.UnixMilli(notAfter).UTC()
	cert.Revoked = revoked != 0
	cert.CreatedAt = time.UnixMilli(createdAt).UTC()
	return cert, nil
}
