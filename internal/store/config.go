package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
)

// ConfigVersion — конфиг Xray, применённый на узле. Из этой таблицы
// делается откат: последняя версия с TestOK — заведомо рабочая.
type ConfigVersion struct {
	NodeID     string    `json:"node_id"`
	Version    int64     `json:"version"`
	Config     string    `json:"config"`
	TestOK     bool      `json:"test_ok"`
	TestOutput string    `json:"test_output,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ConfigVersionsKeep — сколько версий конфига хранить на узел.
const ConfigVersionsKeep = 10

// SaveConfigVersion записывает конфиг очередной версией (MAX+1) и в той же
// транзакции удаляет версии сверх ConfigVersionsKeep. testOK — прошёл ли
// конфиг `xray run -test` и запуск ядра, testOutput — вывод проверки
// при ошибке.
func (s *Store) SaveConfigVersion(ctx context.Context, nodeID, config string, testOK bool, testOutput string) (int64, error) {
	version := int64(0)
	created := s.now().Truncate(time.Second)
	err := s.db.InTx(ctx, func(tx *db.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM config_versions WHERE node_id = ?`, nodeID).Scan(&version); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO config_versions
			(node_id, version, config, test_ok, test_output, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			nodeID, version, config, testOK, testOutput, created.Unix()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM config_versions WHERE node_id = ? AND version NOT IN
			(SELECT version FROM config_versions WHERE node_id = ? ORDER BY version DESC LIMIT ?)`,
			nodeID, nodeID, ConfigVersionsKeep)
		return err
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// ConfigVersions — версии конфига узла, новые первыми; limit <= 0 — все.
func (s *Store) ConfigVersions(ctx context.Context, nodeID string, limit int) ([]ConfigVersion, error) {
	query := `SELECT node_id, version, config, test_ok, test_output, created_at
		FROM config_versions WHERE node_id = ? ORDER BY version DESC`
	if limit > 0 {
		query += ` LIMIT ?`
	}
	args := []any{nodeID}
	if limit > 0 {
		args = append(args, limit)
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConfigVersion{}
	for rows.Next() {
		v, err := scanConfigVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// ConfigVersionByVersion — конкретная версия конфига узла (для отката).
func (s *Store) ConfigVersionByVersion(ctx context.Context, nodeID string, version int64) (*ConfigVersion, error) {
	return scanConfigVersion(s.db.QueryRow(ctx, `SELECT node_id, version, config, test_ok, test_output, created_at
		FROM config_versions WHERE node_id = ? AND version = ?`, nodeID, version))
}

func scanConfigVersion(r scanner) (*ConfigVersion, error) {
	var v ConfigVersion
	var created int64
	if err := r.Scan(&v.NodeID, &v.Version, &v.Config, &v.TestOK, &v.TestOutput, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	v.CreatedAt = time.Unix(created, 0).UTC()
	return &v, nil
}
