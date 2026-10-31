// Package store — данные панели поверх internal/db: узлы, а на следующих
// этапах пользователи, инбаунды и трафик.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
)

// Store — доступ к данным панели.
type Store struct {
	db  *db.DB
	now func() time.Time
}

// New оборачивает открытую и смигрированную базу.
func New(d *db.DB) *Store {
	return &Store{db: d, now: time.Now}
}

// ErrNotFound — записи нет.
var ErrNotFound = errors.New("не найдено")

// NewID — случайный идентификатор записи: 16 байт в hex.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return hex.EncodeToString(b)
}

// Node — сервер, которым управляет панель.
type Node struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Address   string    `json:"address"`
	IsLocal   bool      `json:"is_local"`
	CreatedAt time.Time `json:"created_at"`
}

// LocalNodeName — имя узла панели по умолчанию.
const LocalNodeName = "Этот сервер"

// EnsureLocalNode возвращает локальный узел и создаёт его при первом запуске.
// Так модель «узел» есть с первого дня, и переход к нескольким серверам
// не меняет схему.
func (s *Store) EnsureLocalNode(ctx context.Context) (*Node, error) {
	var n *Node
	err := s.db.InTx(ctx, func(tx *db.Tx) error {
		var err error
		n, err = scanNode(tx.QueryRow(ctx, `SELECT id, name, address, is_local, created_at FROM nodes WHERE is_local = ? ORDER BY created_at LIMIT 1`, true))
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		n = &Node{ID: NewID(), Name: LocalNodeName, IsLocal: true, CreatedAt: s.now().Truncate(time.Second)}
		_, err = tx.Exec(ctx, `INSERT INTO nodes (id, name, address, is_local, created_at) VALUES (?, ?, ?, ?, ?)`,
			n.ID, n.Name, n.Address, n.IsLocal, n.CreatedAt.Unix())
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("локальный узел: %w", err)
	}
	return n, nil
}

// Nodes — все узлы, локальный первым.
func (s *Store) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, address, is_local, created_at FROM nodes ORDER BY is_local DESC, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanNode(r scanner) (*Node, error) {
	var n Node
	var created int64
	if err := r.Scan(&n.ID, &n.Name, &n.Address, &n.IsLocal, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	n.CreatedAt = time.Unix(created, 0).UTC()
	return &n, nil
}
