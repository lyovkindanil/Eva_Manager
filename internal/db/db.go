// Package db открывает базу панели: SQLite (по умолчанию) или PostgreSQL.
//
// Запросы пишутся один раз, с плейсхолдерами "?", и на PostgreSQL
// переписываются в "$1, $2, …" функцией Rebind. Схема в migrations/
// написана на общем подмножестве SQL обеих баз: идентификаторы — TEXT,
// время — BIGINT (секунды Unix), без AUTOINCREMENT и SERIAL.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // драйвер "pgx"
	_ "modernc.org/sqlite"             // драйвер "sqlite", без CGO
)

// Dialect — какая база под панелью.
type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// DB — пул соединений и диалект.
type DB struct {
	*sql.DB
	Dialect Dialect
}

// Open открывает базу по адресу sqlite:///путь или postgres://…, проверяет
// соединение и применяет миграции.
func Open(ctx context.Context, rawURL string) (*DB, error) {
	d, err := open(rawURL)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := d.PingContext(pctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("база %s недоступна: %w", d.Dialect, err)
	}
	if err := d.Migrate(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func open(rawURL string) (*DB, error) {
	scheme, rest, ok := strings.Cut(rawURL, "://")
	if !ok {
		return nil, fmt.Errorf("адрес базы %q: ожидалось sqlite:///путь или postgres://…", rawURL)
	}
	switch strings.ToLower(scheme) {
	case "sqlite", "sqlite3":
		path := rest
		if path == "" {
			return nil, fmt.Errorf("адрес базы %q: не указан путь к файлу", rawURL)
		}
		if path != ":memory:" {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, fmt.Errorf("каталог базы: %w", err)
			}
		}
		// WAL: чтение не ждёт записи; busy_timeout: две записи подряд ждут,
		// а не падают с SQLITE_BUSY; foreign_keys в SQLite по умолчанию выключены.
		q := url.Values{}
		for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
			q.Add("_pragma", p)
		}
		sdb, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
		if err != nil {
			return nil, err
		}
		// Пишет SQLite всё равно по одному; одно соединение на запись
		// убирает SQLITE_BUSY совсем, а нагрузка панели это позволяет.
		sdb.SetMaxOpenConns(1)
		return &DB{DB: sdb, Dialect: SQLite}, nil
	case "postgres", "postgresql":
		sdb, err := sql.Open("pgx", rawURL)
		if err != nil {
			return nil, err
		}
		sdb.SetMaxOpenConns(10)
		sdb.SetConnMaxIdleTime(5 * time.Minute)
		return &DB{DB: sdb, Dialect: Postgres}, nil
	}
	return nil, fmt.Errorf("адрес базы %q: поддерживаются sqlite:// и postgres://", rawURL)
}

// Rebind переписывает плейсхолдеры "?" под диалект. Знак "?" внутри
// строковых литералов запроса не поддерживается: значения передаются
// только параметрами.
func (d *DB) Rebind(query string) string {
	if d.Dialect != Postgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

// Exec, Query и QueryRow — то же, что у database/sql, но с Rebind.

func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.ExecContext(ctx, d.Rebind(query), args...)
}

func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.QueryContext(ctx, d.Rebind(query), args...)
}

func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.QueryRowContext(ctx, d.Rebind(query), args...)
}

// Tx — транзакция с тем же Rebind.
type Tx struct {
	*sql.Tx
	db *DB
}

func (t *Tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.ExecContext(ctx, t.db.Rebind(query), args...)
}

func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return t.QueryRowContext(ctx, t.db.Rebind(query), args...)
}

// InTx выполняет fn в транзакции: ошибка или паника откатывают её.
func (d *DB) InTx(ctx context.Context, fn func(*Tx) error) (err error) {
	stx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	tx := &Tx{Tx: stx, db: d}
	defer func() {
		if p := recover(); p != nil {
			_ = stx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = stx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return stx.Commit()
}
