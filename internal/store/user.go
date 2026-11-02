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

// UserStatus — статус пользователя (столбец users.status).
type UserStatus string

const (
	UserActive   UserStatus = "active"   // работает
	UserDisabled UserStatus = "disabled" // выключен вручную
	UserExpired  UserStatus = "expired"  // истёк срок
	UserLimited  UserStatus = "limited"  // кончился трафик
)

// User — пользователь панели. В Xray он попадает с email = ID: по нему
// StatsService отдаёт его трафик.
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	SubToken string `json:"sub_token"`
	// TrafficLimit — лимит в байтах, 0 — без лимита.
	TrafficLimit int64 `json:"traffic_limit"`
	TrafficUsed  int64 `json:"traffic_used"`
	// ExpiresAt — конец действия; нулевое время — бессрочно.
	ExpiresAt time.Time `json:"expires_at"`
	Status    UserStatus `json:"status"`
	Note      string     `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// ComputeStatus — статус по фактам на момент now. Ручное отключение
// сохраняется; срок и лимит пересчитываются, поэтому продление (новый
// срок, сброс трафика) само возвращает пользователя в строй.
func (u User) ComputeStatus(now time.Time) UserStatus {
	if u.Status == UserDisabled {
		return UserDisabled
	}
	if !u.ExpiresAt.IsZero() && !now.Before(u.ExpiresAt) {
		return UserExpired
	}
	if u.TrafficLimit > 0 && u.TrafficUsed >= u.TrafficLimit {
		return UserLimited
	}
	return UserActive
}

// NewUUID — UUID пользователя (как в ссылках vless://).
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	b[6] = b[6]&0x0f | 0x40 // версия 4
	b[8] = b[8]&0x3f | 0x80 // вариант RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newSubToken — токен подписки: 128 случайных бит в hex. Перевыпуск —
// одной кнопкой (этап 3).
func newSubToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return hex.EncodeToString(b)
}

// CreateUser создаёт пользователя: UUID и токен подписки генерируются.
// trafficLimit в байтах (0 — без лимита), нулевое expiresAt — бессрочно.
// Доступ открывается ко всем включённым инбаундам локального узла.
func (s *Store) CreateUser(ctx context.Context, nodeID, name, note string, trafficLimit int64, expiresAt time.Time) (*User, error) {
	if name == "" {
		return nil, errors.New("пустое имя пользователя")
	}
	u := &User{
		ID:           NewID(),
		Name:         name,
		UUID:         NewUUID(),
		SubToken:     newSubToken(),
		TrafficLimit: trafficLimit,
		ExpiresAt:    expiresAt.UTC().Truncate(time.Second),
		Status:       UserActive,
		Note:         note,
		CreatedAt:    s.now().Truncate(time.Second),
	}
	err := s.db.InTx(ctx, func(tx *db.Tx) error {
		var expires int64
		if !u.ExpiresAt.IsZero() {
			expires = u.ExpiresAt.Unix()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO users
			(id, name, uuid, sub_token, traffic_limit, traffic_used, expires_at, status, note, created_at)
			VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
			u.ID, u.Name, u.UUID, u.SubToken, u.TrafficLimit, expires, string(u.Status), u.Note, u.CreatedAt.Unix()); err != nil {
			return err
		}
		// Доступ ко всем включённым инбаундам узла.
		_, err := tx.Exec(ctx, `INSERT INTO user_inbounds (user_id, inbound_id)
			SELECT ?, id FROM inbounds WHERE node_id = ? AND enabled`, u.ID, nodeID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("создать пользователя: %w", err)
	}
	return u, nil
}

// Users — все пользователи по алфавиту.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, uuid, sub_token, traffic_limit, traffic_used,
		expires_at, status, note, created_at FROM users ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UserByID — пользователь по id.
func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT id, name, uuid, sub_token, traffic_limit, traffic_used,
		expires_at, status, note, created_at FROM users WHERE id = ?`, id))
}

// UserByName — пользователь по имени.
func (s *Store) UserByName(ctx context.Context, name string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT id, name, uuid, sub_token, traffic_limit, traffic_used,
		expires_at, status, note, created_at FROM users WHERE name = ?`, name))
}

// UserBySubToken — пользователь по токену подписки (для /sub/<токен>).
func (s *Store) UserBySubToken(ctx context.Context, token string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT id, name, uuid, sub_token, traffic_limit, traffic_used,
		expires_at, status, note, created_at FROM users WHERE sub_token = ?`, token))
}

// SetUserStatus записывает статус (его пересчитывает сборщик трафика
// по ComputeStatus).
func (s *Store) SetUserStatus(ctx context.Context, id string, status UserStatus) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET status = ? WHERE id = ?`, string(status), id)
	return err
}

// SetUserDisabled — ручное включение/выключение.
func (s *Store) SetUserDisabled(ctx context.Context, id string, disabled bool) error {
	st := UserActive
	if disabled {
		st = UserDisabled
	}
	return s.SetUserStatus(ctx, id, st)
}

// SetUserLimits меняет лимит трафика (байты, 0 — без лимита) и срок
// (нулевое expiresAt — бессрочно).
func (s *Store) SetUserLimits(ctx context.Context, id string, trafficLimit int64, expiresAt time.Time) error {
	var expires int64
	if !expiresAt.IsZero() {
		expires = expiresAt.UTC().Unix()
	}
	_, err := s.db.Exec(ctx, `UPDATE users SET traffic_limit = ?, expires_at = ? WHERE id = ?`, trafficLimit, expires, id)
	return err
}

// ResetUserTraffic обнуляет израсходованный трафик: пользователь с
// статусом «лимит» вернётся в строй при следующем пересчёте.
func (s *Store) ResetUserTraffic(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET traffic_used = 0 WHERE id = ?`, id)
	return err
}

// DeleteUser удаляет пользователя (доступы и суточная статистика —
// каскадом).
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

// AddUserTraffic добавляет трафик пользователю: в общий счётчик идёт
// сумма входящего и исходящего, в суточную статистику — раздельно.
// day — 'YYYY-MM-DD' в UTC.
func (s *Store) AddUserTraffic(ctx context.Context, id, day string, up, down int64) error {
	return s.db.InTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET traffic_used = traffic_used + ? WHERE id = ?`, up+down, id); err != nil {
			return err
		}
		// upsert в общем подмножестве SQLite и PostgreSQL.
		if _, err := tx.Exec(ctx, `UPDATE traffic_daily SET uplink = uplink + ?, downlink = downlink + ?
			WHERE user_id = ? AND day = ?`, up, down, id, day); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO traffic_daily (user_id, day, uplink, downlink)
			SELECT ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM traffic_daily WHERE user_id = ? AND day = ?)`,
			id, day, up, down, id, day)
		return err
	})
}

// TrafficDay — трафик пользователя за сутки UTC.
type TrafficDay struct {
	Day      string `json:"day"`
	Uplink   int64  `json:"uplink"`
	Downlink int64  `json:"downlink"`
}

// UserTrafficDaily — суточная статистика пользователя, новые дни первыми.
func (s *Store) UserTrafficDaily(ctx context.Context, id string, days int) ([]TrafficDay, error) {
	rows, err := s.db.Query(ctx, `SELECT day, uplink, downlink FROM traffic_daily
		WHERE user_id = ? ORDER BY day DESC LIMIT ?`, id, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficDay{}
	for rows.Next() {
		var t TrafficDay
		if err := rows.Scan(&t.Day, &t.Uplink, &t.Downlink); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UserInboundIDs — инбаунды, к которым у пользователя есть доступ.
func (s *Store) UserInboundIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT inbound_id FROM user_inbounds WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ActiveUsersForInbound — активные пользователи с доступом к инбаунду:
// именно они должны быть в ядре.
func (s *Store) ActiveUsersForInbound(ctx context.Context, inboundID string) ([]User, error) {
	rows, err := s.db.Query(ctx, `SELECT u.id, u.name, u.uuid, u.sub_token, u.traffic_limit, u.traffic_used,
		u.expires_at, u.status, u.note, u.created_at FROM users u
		JOIN user_inbounds ui ON ui.user_id = u.id
		WHERE ui.inbound_id = ? AND u.status = ? ORDER BY u.name, u.id`, inboundID, string(UserActive))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func scanUser(r scanner) (*User, error) {
	var u User
	var expires, created int64
	var status string
	err := r.Scan(&u.ID, &u.Name, &u.UUID, &u.SubToken, &u.TrafficLimit, &u.TrafficUsed,
		&expires, &status, &u.Note, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if expires != 0 {
		u.ExpiresAt = time.Unix(expires, 0).UTC()
	}
	u.Status = UserStatus(status)
	u.CreatedAt = time.Unix(created, 0).UTC()
	return &u, nil
}
