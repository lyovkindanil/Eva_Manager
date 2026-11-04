package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
)

// Admin — администратор панели.
type Admin struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrAdminExists — первый администратор уже создан.
var ErrAdminExists = errors.New("администратор уже создан")

// ErrBadLogin — неверные имя или пароль (без уточнения, что именно).
var ErrBadLogin = errors.New("неверное имя или пароль")

// MinPasswordLen — короче пароль не принимается.
const MinPasswordLen = 10

// Параметры argon2id: 64 МиБ, 3 прохода — порядка 0,1 с на вход.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
)

// HashPassword — argon2id в формате PHC ($argon2id$v=19$m=…,t=…,p=…$соль$хеш).
func HashPassword(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, 32)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		enc.EncodeToString(salt), enc.EncodeToString(key))
}

// CheckPassword сверяет пароль с хешем HashPassword.
func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 || m > 1<<21 || t > 64 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[4])
	want, err2 := enc.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func checkCredentials(username, password string) error {
	if strings.TrimSpace(username) == "" || len(username) > 64 || strings.ContainsAny(username, " \t\n") {
		return errors.New("имя администратора: без пробелов, до 64 символов")
	}
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return fmt.Errorf("пароль не короче %d символов", MinPasswordLen)
	}
	return nil
}

// AdminCount — сколько администраторов (0 — нужен первый запуск).
func (s *Store) AdminCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

// CreateFirstAdmin создаёт первого администратора; если он уже есть —
// ErrAdminExists (мастер первого запуска не перехватить повторно).
func (s *Store) CreateFirstAdmin(ctx context.Context, username, password string) (*Admin, error) {
	if err := checkCredentials(username, password); err != nil {
		return nil, err
	}
	a := &Admin{ID: NewID(), Username: username, CreatedAt: s.now().Truncate(time.Second)}
	hash := HashPassword(password)
	err := s.db.InTx(ctx, func(tx *db.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrAdminExists
		}
		_, err := tx.Exec(ctx, `INSERT INTO admins (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)`,
			a.ID, a.Username, hash, a.CreatedAt.Unix())
		return err
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// SetAdminPassword задаёт пароль администратору, создавая его, если нет
// (eva-manager admin — восстановление доступа с сервера). Все его сессии
// закрываются.
func (s *Store) SetAdminPassword(ctx context.Context, username, password string) (*Admin, bool, error) {
	if err := checkCredentials(username, password); err != nil {
		return nil, false, err
	}
	hash := HashPassword(password)
	var a *Admin
	created := false
	err := s.db.InTx(ctx, func(tx *db.Tx) error {
		var err error
		a, err = scanAdmin(tx.QueryRow(ctx, `SELECT id, username, created_at FROM admins WHERE username = ?`, username))
		if errors.Is(err, ErrNotFound) {
			a = &Admin{ID: NewID(), Username: username, CreatedAt: s.now().Truncate(time.Second)}
			created = true
			_, err = tx.Exec(ctx, `INSERT INTO admins (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)`,
				a.ID, a.Username, hash, a.CreatedAt.Unix())
			return err
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE admins SET password_hash = ? WHERE id = ?`, hash, a.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE admin_id = ?`, a.ID)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return a, created, nil
}

// dummyHash — сверка с ним для несуществующего имени: время ответа не
// выдаёт, есть ли такой администратор.
var dummyHash = HashPassword("eva-manager-dummy-password")

// Login проверяет имя и пароль.
func (s *Store) Login(ctx context.Context, username, password string) (*Admin, error) {
	var a Admin
	var hash string
	var created int64
	err := s.db.QueryRow(ctx, `SELECT id, username, password_hash, created_at FROM admins WHERE username = ?`, username).
		Scan(&a.ID, &a.Username, &hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		CheckPassword(dummyHash, password)
		return nil, ErrBadLogin
	}
	if err != nil {
		return nil, err
	}
	if !CheckPassword(hash, password) {
		return nil, ErrBadLogin
	}
	a.CreatedAt = time.Unix(created, 0).UTC()
	return &a, nil
}

// SessionTTL — сколько живёт вход.
const SessionTTL = 14 * 24 * time.Hour

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// CreateSession — новая сессия администратора; возвращает токен для
// cookie (в базе — только его хеш).
func (s *Store) CreateSession(ctx context.Context, adminID string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	exp := now.Add(SessionTTL).Truncate(time.Second)
	// Попутно — уборка истёкших.
	if _, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", time.Time{}, err
	}
	_, err := s.db.Exec(ctx, `INSERT INTO sessions (token_hash, admin_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(tok), adminID, now.Unix(), exp.Unix())
	return tok, exp, err
}

// SessionAdmin — администратор по токену живой сессии; ErrNotFound — нет
// или истекла.
func (s *Store) SessionAdmin(ctx context.Context, tok string) (*Admin, error) {
	if tok == "" {
		return nil, ErrNotFound
	}
	return scanAdmin(s.db.QueryRow(ctx, `SELECT a.id, a.username, a.created_at FROM sessions s
		JOIN admins a ON a.id = s.admin_id WHERE s.token_hash = ? AND s.expires_at > ?`, hashToken(tok), s.now().Unix()))
}

// DeleteSession — выход.
func (s *Store) DeleteSession(ctx context.Context, tok string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(tok))
	return err
}

func scanAdmin(r scanner) (*Admin, error) {
	var a Admin
	var created int64
	if err := r.Scan(&a.ID, &a.Username, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.CreatedAt = time.Unix(created, 0).UTC()
	return &a, nil
}

// Setting — значение из таблицы settings; "" — не задано.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting записывает значение; пустое — удаляет.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.db.InTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			return err
		}
		if value == "" {
			return nil
		}
		_, err := tx.Exec(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, key, value)
		return err
	})
}
