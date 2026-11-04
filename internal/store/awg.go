package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
)

const awgCols = `id, node_id, name, iface, port, link_host, enabled, settings`

// CreateAWG сохраняет интерфейс AmneziaWG и выдаёт пиры всем
// пользователям: у каждого свой ключ и адрес в подсети.
func (s *Store) CreateAWG(ctx context.Context, in awg.Interface) error {
	if err := in.Validate(); err != nil {
		return err
	}
	settings, err := json.Marshal(in.Settings)
	if err != nil {
		return err
	}
	now := s.now().Truncate(time.Second).Unix()
	return s.db.InTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO awg_interfaces
			(id, node_id, name, iface, port, link_host, enabled, settings, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.ID, in.NodeID, in.Name, in.Iface, in.Port, in.LinkHost, in.Enabled, string(settings), now); err != nil {
			return fmt.Errorf("AmneziaWG %q: %w", in.Name, err)
		}
		ids, err := queryIDs(ctx, tx, `SELECT id FROM users ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		return addPeers(ctx, tx, in, ids, now)
	})
}

// addPeers выдаёт пиры пользователям на интерфейсе (тем, у кого его нет).
func addPeers(ctx context.Context, tx *db.Tx, in awg.Interface, userIDs []string, now int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT user_id, address FROM awg_peers WHERE interface_id = ?`, in.ID)
	if err != nil {
		return err
	}
	used := map[netip.Addr]bool{}
	has := map[string]bool{}
	for rows.Next() {
		var uid, addr string
		if err := rows.Scan(&uid, &addr); err != nil {
			rows.Close()
			return err
		}
		has[uid] = true
		if a, err := netip.ParseAddr(addr); err == nil {
			used[a] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, uid := range userIDs {
		if has[uid] {
			continue
		}
		addr, ok := in.FreeAddr(used)
		if !ok {
			return fmt.Errorf("AmneziaWG %q: в подсети %s кончились адреса", in.Name, in.Settings.Subnet)
		}
		used[addr] = true
		priv, err := awg.NewKey()
		if err != nil {
			return err
		}
		pub, _ := awg.PublicKey(priv)
		if _, err := tx.Exec(ctx, `INSERT INTO awg_peers
			(interface_id, user_id, private_key, public_key, preshared_key, address, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, in.ID, uid, priv, pub, awg.NewPSK(), addr.String(), now); err != nil {
			return err
		}
	}
	return nil
}

func queryIDs(ctx context.Context, tx *db.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// addUserPeers — пиры нового пользователя на всех интерфейсах узла.
func addUserPeers(ctx context.Context, tx *db.Tx, nodeID, userID string, now int64) error {
	ifs, err := scanAWGs(tx.Query(ctx, `SELECT `+awgCols+` FROM awg_interfaces WHERE node_id = ? ORDER BY created_at, id`, nodeID))
	if err != nil {
		return err
	}
	for _, in := range ifs {
		if err := addPeers(ctx, tx, in, []string{userID}, now); err != nil {
			return err
		}
	}
	return nil
}

// AWGInterfaces — интерфейсы узла (пустой nodeID — всех), включённые первыми.
func (s *Store) AWGInterfaces(ctx context.Context, nodeID string) ([]awg.Interface, error) {
	q := `SELECT ` + awgCols + ` FROM awg_interfaces`
	args := []any{}
	if nodeID != "" {
		q += ` WHERE node_id = ?`
		args = append(args, nodeID)
	}
	return scanAWGs(s.db.Query(ctx, q+` ORDER BY enabled DESC, created_at, id`, args...))
}

// AWGByName — интерфейс узла по имени (подписи) или имени сетевого
// интерфейса.
func (s *Store) AWGByName(ctx context.Context, nodeID, name string) (*awg.Interface, error) {
	return scanAWG(s.db.QueryRow(ctx, `SELECT `+awgCols+` FROM awg_interfaces
		WHERE node_id = ? AND (name = ? OR iface = ?) ORDER BY name = ? DESC LIMIT 1`, nodeID, name, name, name))
}

// SetAWGEnabled включает/выключает интерфейс.
func (s *Store) SetAWGEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.Exec(ctx, `UPDATE awg_interfaces SET enabled = ? WHERE id = ?`, enabled, id)
	return err
}

// DeleteAWG удаляет интерфейс (пиры — каскадом).
func (s *Store) DeleteAWG(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM awg_interfaces WHERE id = ?`, id)
	return err
}

const peerCols = `p.interface_id, p.user_id, p.private_key, p.preshared_key, p.address`

// ActiveAWGPeers — пиры активных пользователей: именно они должны быть
// на сервере.
func (s *Store) ActiveAWGPeers(ctx context.Context, interfaceID string) ([]awg.Peer, error) {
	return scanPeers(s.db.Query(ctx, `SELECT `+peerCols+` FROM awg_peers p JOIN users u ON u.id = p.user_id
		WHERE p.interface_id = ? AND u.status = ? ORDER BY p.address`, interfaceID, string(UserActive)))
}

// AWGPeerUsers — public_key → user_id всех пиров интерфейса (для счётчиков).
func (s *Store) AWGPeerUsers(ctx context.Context, interfaceID string) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT public_key, user_id FROM awg_peers WHERE interface_id = ?`, interfaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, u string
		if err := rows.Scan(&k, &u); err != nil {
			return nil, err
		}
		out[k] = u
	}
	return out, rows.Err()
}

// UserAWG — пир пользователя на включённом интерфейсе.
type UserAWG struct {
	Interface awg.Interface
	Peer      awg.Peer
}

// UserAWGs — включённые интерфейсы пользователя с его пирами: из них
// собираются конфигурации и подписка.
func (s *Store) UserAWGs(ctx context.Context, userID string) ([]UserAWG, error) {
	rows, err := s.db.Query(ctx, `SELECT i.id, i.node_id, i.name, i.iface, i.port, i.link_host, i.enabled, i.settings, `+peerCols+`
		FROM awg_interfaces i JOIN awg_peers p ON p.interface_id = i.id
		WHERE p.user_id = ? AND i.enabled ORDER BY i.created_at, i.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserAWG{}
	for rows.Next() {
		var ua UserAWG
		var settings, addr string
		in := &ua.Interface
		p := &ua.Peer
		if err := rows.Scan(&in.ID, &in.NodeID, &in.Name, &in.Iface, &in.Port, &in.LinkHost, &in.Enabled, &settings,
			&p.InterfaceID, &p.UserID, &p.PrivateKey, &p.PresharedKey, &addr); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(settings), &in.Settings); err != nil {
			return nil, fmt.Errorf("AmneziaWG %s: settings: %w", in.Name, err)
		}
		if p.Address, err = netip.ParseAddr(addr); err != nil {
			return nil, fmt.Errorf("AmneziaWG %s: адрес пира %q: %w", in.Name, addr, err)
		}
		out = append(out, ua)
	}
	return out, rows.Err()
}

func scanPeers(rows *sql.Rows, err error) ([]awg.Peer, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []awg.Peer{}
	for rows.Next() {
		var p awg.Peer
		var addr string
		if err := rows.Scan(&p.InterfaceID, &p.UserID, &p.PrivateKey, &p.PresharedKey, &addr); err != nil {
			return nil, err
		}
		if p.Address, err = netip.ParseAddr(addr); err != nil {
			return nil, fmt.Errorf("адрес пира %q: %w", addr, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanAWGs(rows *sql.Rows, err error) ([]awg.Interface, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []awg.Interface{}
	for rows.Next() {
		in, err := scanAWG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rows.Err()
}

func scanAWG(r scanner) (*awg.Interface, error) {
	var in awg.Interface
	var settings string
	if err := r.Scan(&in.ID, &in.NodeID, &in.Name, &in.Iface, &in.Port, &in.LinkHost, &in.Enabled, &settings); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(settings), &in.Settings); err != nil {
		return nil, fmt.Errorf("AmneziaWG %s: settings: %w", in.Name, err)
	}
	return &in, nil
}
