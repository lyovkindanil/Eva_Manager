package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
)

func TestRebind(t *testing.T) {
	pg := &db.DB{Dialect: db.Postgres}
	if got := pg.Rebind("SELECT ? , ?, x FROM t WHERE a = ?"); got != "SELECT $1 , $2, x FROM t WHERE a = $3" {
		t.Errorf("postgres: %q", got)
	}
	lite := &db.DB{Dialect: db.SQLite}
	if got := lite.Rebind("a = ?"); got != "a = ?" {
		t.Errorf("sqlite: %q", got)
	}
}

func TestOpenBadURL(t *testing.T) {
	for _, u := range []string{"eva.db", "mysql://x", "sqlite://"} {
		if _, err := db.Open(context.Background(), u); err == nil {
			t.Errorf("%q принят", u)
		}
	}
}

func TestMigrateAndSchema(t *testing.T) {
	ms, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Version != 1 {
		t.Fatalf("миграции: %+v", ms)
	}
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		v, err := d.SchemaVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if v != ms[len(ms)-1].Version {
			t.Errorf("версия схемы %d, ожидалась %d", v, ms[len(ms)-1].Version)
		}
		// Повторный запуск ничего не делает.
		if err := d.Migrate(ctx); err != nil {
			t.Fatalf("повторная миграция: %v", err)
		}
		// Все таблицы на месте и принимают одни и те же запросы.
		for _, table := range []string{"nodes", "users", "inbounds", "user_inbounds", "traffic_daily", "config_versions", "admins", "settings"} {
			var n int
			if err := d.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
				t.Errorf("%s: %v", table, err)
			}
		}
		// Внешние ключи включены в обеих базах (в SQLite — прагмой).
		_, err = d.Exec(ctx, `INSERT INTO inbounds (id, node_id, name, protocol, port, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"in1", "нет-такого-узла", "x", "vless", 443, 1)
		if err == nil {
			t.Error("инбаунд без узла принят: внешние ключи выключены")
		}
		// BOOLEAN читается в bool в обеих базах.
		if _, err := d.Exec(ctx, `INSERT INTO nodes (id, name, is_local, created_at) VALUES (?, ?, ?, ?)`, "n1", "local", true, 1); err != nil {
			t.Fatal(err)
		}
		var local bool
		if err := d.QueryRow(ctx, `SELECT is_local FROM nodes WHERE id = ?`, "n1").Scan(&local); err != nil || !local {
			t.Errorf("is_local = %v, %v", local, err)
		}
	})
}

func TestMigrationRollsBack(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		before, _ := d.SchemaVersion(ctx)
		bad := []db.Migration{{Version: before + 1, Name: "broken", SQL: "CREATE TABLE half (id TEXT); SELECT * FROM нет_такой_таблицы;"}}
		err := db.MigrateWith(ctx, d, bad)
		if err == nil || !strings.Contains(err.Error(), "broken") {
			t.Fatalf("err = %v", err)
		}
		if v, _ := d.SchemaVersion(ctx); v != before {
			t.Errorf("версия %d после упавшей миграции, ожидалась %d", v, before)
		}
		var n int
		if err := d.QueryRow(ctx, "SELECT COUNT(*) FROM half").Scan(&n); err == nil {
			t.Error("таблица из упавшей миграции осталась")
		}
	})
}

func TestNewerSchemaRefused(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`, 9999, "future", 1); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "новее") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadMigrationsNames(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0002_b.sql": {Data: []byte("b")},
		"m/0001_a.sql": {Data: []byte("a")},
		"m/readme.txt": {Data: []byte("x")},
	}
	ms, err := db.LoadMigrations(fsys, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Name != "a" || ms[1].Version != 2 {
		t.Errorf("%+v", ms)
	}
	if _, err := db.LoadMigrations(fstest.MapFS{"m/x.sql": {}}, "m"); err == nil {
		t.Error("имя без номера принято")
	}
	if _, err := db.LoadMigrations(fstest.MapFS{"m/0001_a.sql": {}, "m/0001_b.sql": {}}, "m"); err == nil {
		t.Error("одинаковые номера приняты")
	}
}

func TestInTxRollback(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	boom := errors.New("boom")
	err := d.InTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, "k", "v"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	var n int
	if err := d.QueryRow(ctx, `SELECT COUNT(*) FROM settings`).Scan(&n); err != nil || n != 0 {
		t.Errorf("n = %d, %v: запись из откаченной транзакции осталась", n, err)
	}
}
