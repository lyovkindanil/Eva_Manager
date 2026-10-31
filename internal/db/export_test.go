package db

import (
	"context"
	"io/fs"
)

// Для тестов из пакета db_test.

func MigrateWith(ctx context.Context, d *DB, ms []Migration) error { return d.migrate(ctx, ms) }

func LoadMigrations(fsys fs.FS, dir string) ([]Migration, error) { return loadMigrations(fsys, dir) }
