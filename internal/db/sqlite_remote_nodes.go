package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) CreateRemoteNode(ctx context.Context, node *models.RemoteNode) error {
	now := time.Now().UTC()
	node.CreatedAt = now
	node.UpdatedAt = now
	if node.Port == 0 {
		node.Port = 22
	}
	if node.AuthType == "" {
		node.AuthType = "private_key"
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO remote_nodes (id, user_id, name, host, port, username, auth_type,
		 credential_secret_id, known_hosts, base_path, enabled, last_check_status,
		 last_check_error, last_checked_at, created_at, updated_at)
		 VALUES (:id, :user_id, :name, :host, :port, :username, :auth_type,
		 :credential_secret_id, :known_hosts, :base_path, :enabled, :last_check_status,
		 :last_check_error, :last_checked_at, :created_at, :updated_at)`, node)
	return err
}

func (s *SQLiteStore) GetRemoteNodeByID(ctx context.Context, id string) (*models.RemoteNode, error) {
	var node models.RemoteNode
	err := s.db.GetContext(ctx, &node, "SELECT * FROM remote_nodes WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func (s *SQLiteStore) ListRemoteNodesByUser(ctx context.Context, userID string) ([]models.RemoteNode, error) {
	var nodes []models.RemoteNode
	err := s.db.SelectContext(ctx, &nodes, "SELECT * FROM remote_nodes WHERE user_id = ? ORDER BY created_at DESC", userID)
	return nodes, err
}

func (s *SQLiteStore) UpdateRemoteNode(ctx context.Context, node *models.RemoteNode) error {
	node.UpdatedAt = time.Now().UTC()
	if node.Port == 0 {
		node.Port = 22
	}
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE remote_nodes SET name=:name, host=:host, port=:port, username=:username,
		 auth_type=:auth_type, credential_secret_id=:credential_secret_id, known_hosts=:known_hosts,
		 base_path=:base_path, enabled=:enabled, updated_at=:updated_at WHERE id=:id`, node)
	return err
}

func (s *SQLiteStore) DeleteRemoteNode(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM remote_nodes WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) TouchRemoteNodeCheck(ctx context.Context, id, status, checkError string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE remote_nodes SET last_check_status = ?, last_check_error = ?, last_checked_at = ?, updated_at = ? WHERE id = ?`,
		status, checkError, now, now, id)
	return err
}
