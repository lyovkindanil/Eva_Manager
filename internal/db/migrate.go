package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration — один файл migrations/NNNN_имя.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations возвращает встроенные миграции по возрастанию версии.
func Migrations() ([]Migration, error) {
	return loadMigrations(migrationFS, "migrations")
}

func loadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var ms []Migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("миграция %s: имя должно быть вида 0001_имя.sql", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("миграции %s и %s: одинаковый номер %d", prev, name, v)
		}
		seen[v] = name
		body, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, Migration{Version: v, Name: rest, SQL: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms, nil
}

// Migrate применяет ещё не применённые миграции, каждую в своей транзакции.
// Обе базы умеют DDL в транзакции, так что упавшая миграция не оставляет
// схему наполовину изменённой.
func (d *DB) Migrate(ctx context.Context) error {
	ms, err := Migrations()
	if err != nil {
		return err
	}
	return d.migrate(ctx, ms)
}

func (d *DB) migrate(ctx context.Context, ms []Migration) error {
	if _, err := d.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at BIGINT NOT NULL
	)`); err != nil {
		return fmt.Errorf("таблица миграций: %w", err)
	}
	cur, err := d.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if n := len(ms); n > 0 && cur > ms[n-1].Version {
		return fmt.Errorf("схема базы версии %d новее этой сборки панели (%d): обновите панель", cur, ms[n-1].Version)
	}
	for _, m := range ms {
		if m.Version <= cur {
			continue
		}
		err := d.InTx(ctx, func(tx *Tx) error {
			if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				m.Version, m.Name, time.Now().Unix())
			return err
		})
		if err != nil {
			return fmt.Errorf("миграция %04d_%s: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

// SchemaVersion — номер последней применённой миграции, 0 — пустая база.
func (d *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := d.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("версия схемы: %w", err)
	}
	return v, nil
}
