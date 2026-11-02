package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
)

// CreateInbound сохраняет инбаунд (типизированные параметры — JSON в
// settings) и открывает к нему доступ всем существующим пользователям.
func (s *Store) CreateInbound(ctx context.Context, in inbound.Inbound) error {
	if err := in.Validate(); err != nil {
		return err
	}
	settings, err := json.Marshal(in.Reality)
	if err != nil {
		return err
	}
	created := s.now().Truncate(time.Second)
	return s.db.InTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO inbounds
			(id, node_id, name, protocol, port, settings, link_host, enabled, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.ID, in.NodeID, in.Name, in.Protocol, in.Port, string(settings), in.LinkHost, in.Enabled, created.Unix()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_inbounds (user_id, inbound_id)
			SELECT id, ? FROM users`, in.ID)
		return err
	})
}

// Inbounds — инбаунды узла (пустой nodeID — всех узлов), включённые
// первыми.
func (s *Store) Inbounds(ctx context.Context, nodeID string) ([]inbound.Inbound, error) {
	query := `SELECT id, node_id, name, protocol, port, settings, link_host, enabled FROM inbounds`
	args := []any{}
	if nodeID != "" {
		query += ` WHERE node_id = ?`
		args = append(args, nodeID)
	}
	query += ` ORDER BY enabled DESC, created_at, id`
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []inbound.Inbound{}
	for rows.Next() {
		in, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rows.Err()
}

// InboundByID — инбаунд по id.
func (s *Store) InboundByID(ctx context.Context, id string) (*inbound.Inbound, error) {
	return scanInbound(s.db.QueryRow(ctx, `SELECT id, node_id, name, protocol, port, settings, link_host, enabled
		FROM inbounds WHERE id = ?`, id))
}

// InboundByName — инбаунд по имени узла.
func (s *Store) InboundByName(ctx context.Context, nodeID, name string) (*inbound.Inbound, error) {
	return scanInbound(s.db.QueryRow(ctx, `SELECT id, node_id, name, protocol, port, settings, link_host, enabled
		FROM inbounds WHERE node_id = ? AND name = ?`, nodeID, name))
}

// SetInboundEnabled включает/выключает инбаунд. Изменение попадает в ядро
// при следующем применении конфига (eva-manager apply).
func (s *Store) SetInboundEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.Exec(ctx, `UPDATE inbounds SET enabled = ? WHERE id = ?`, enabled, id)
	return err
}

// DeleteInbound удаляет инбаунд (доступы пользователей — каскадом).
func (s *Store) DeleteInbound(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM inbounds WHERE id = ?`, id)
	return err
}

// GrantInboundUsers открывает доступ к инбаунду пользователям (без дублей).
func (s *Store) GrantInboundUsers(ctx context.Context, inboundID string, userIDs ...string) error {
	return s.db.InTx(ctx, func(tx *db.Tx) error {
		for _, uid := range userIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO user_inbounds (user_id, inbound_id)
				SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM user_inbounds WHERE user_id = ? AND inbound_id = ?)`,
				uid, inboundID, uid, inboundID); err != nil {
				return err
			}
		}
		return nil
	})
}

func scanInbound(r scanner) (*inbound.Inbound, error) {
	var in inbound.Inbound
	var settings string
	err := r.Scan(&in.ID, &in.NodeID, &in.Name, &in.Protocol, &in.Port, &settings, &in.LinkHost, &in.Enabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(settings), &in.Reality); err != nil {
		return nil, fmt.Errorf("инбаунд %s: settings: %w", in.Name, err)
	}
	return &in, nil
}
