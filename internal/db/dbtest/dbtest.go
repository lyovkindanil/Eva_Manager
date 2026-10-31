// Package dbtest открывает тестовые базы: SQLite всегда, PostgreSQL — если
// задан EVA_TEST_POSTGRES_URL (так делает CI). Каждый тест получает свою
// пустую базу: в PostgreSQL — свою схему, удаляемую после теста.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
)

// EnvPostgres — адрес PostgreSQL для тестов.
const EnvPostgres = "EVA_TEST_POSTGRES_URL"

// Each запускает fn как подтест для каждой доступной базы.
func Each(t *testing.T, fn func(t *testing.T, d *db.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, SQLite(t)) })
	// Без EVA_TEST_POSTGRES_URL подтест пропускается явно (SKIP в выводе).
	t.Run("postgres", func(t *testing.T) { fn(t, Postgres(t)) })
}

// SQLite — пустая база в файле во временном каталоге теста.
func SQLite(t *testing.T) *db.DB {
	t.Helper()
	return mustOpen(t, "sqlite://"+filepath.Join(t.TempDir(), "eva.db"))
}

// Postgres — пустая база в отдельной схеме; тест пропускается без EVA_TEST_POSTGRES_URL.
func Postgres(t *testing.T) *db.DB {
	t.Helper()
	base := os.Getenv(EnvPostgres)
	if base == "" {
		t.Skipf("не задан %s", EnvPostgres)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(b)
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("схема для теста: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Errorf("удаление схемы %s: %v", schema, err)
		}
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return mustOpen(t, u.String())
}

func mustOpen(t *testing.T, rawURL string) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), rawURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
