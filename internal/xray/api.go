package xray

import (
	"context"
	"fmt"
	"strings"
	"time"

	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// API — клиент gRPC API Xray. Соединение без TLS: API слушает только
// 127.0.0.1 (это проверяет config).
type API struct {
	conn    *grpc.ClientConn
	stats   statscmd.StatsServiceClient
	handler handlercmd.HandlerServiceClient
}

// Dial готовит клиент; само соединение устанавливается при первом вызове.
func Dial(addr string) (*API, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("api xray %s: %w", addr, err)
	}
	return &API{conn: conn, stats: statscmd.NewStatsServiceClient(conn), handler: handlercmd.NewHandlerServiceClient(conn)}, nil
}

// Close закрывает соединение.
func (a *API) Close() error { return a.conn.Close() }

// SysStats — состояние процесса Xray.
type SysStats struct {
	Uptime     time.Duration `json:"uptime"`
	Goroutines uint32        `json:"goroutines"`
	MemAlloc   uint64        `json:"mem_alloc"`
}

// SysStats спрашивает у Xray время работы и память; заодно это проверка,
// что API отвечает.
func (a *API) SysStats(ctx context.Context) (*SysStats, error) {
	r, err := a.stats.GetSysStats(ctx, &statscmd.SysStatsRequest{})
	if err != nil {
		return nil, fmt.Errorf("api xray: %w", err)
	}
	return &SysStats{
		Uptime:     time.Duration(r.Uptime) * time.Second,
		Goroutines: r.NumGoroutine,
		MemAlloc:   r.Alloc,
	}, nil
}

// Traffic — байты пользователя с прошлого сброса счётчиков.
type Traffic struct {
	Uplink   int64 `json:"uplink"`
	Downlink int64 `json:"downlink"`
}

// UserTraffic — трафик по email пользователей. reset обнуляет счётчики в
// Xray: так панель забирает приращения и не теряет их при перезапуске Xray.
func (a *API) UserTraffic(ctx context.Context, reset bool) (map[string]Traffic, error) {
	r, err := a.stats.QueryStats(ctx, &statscmd.QueryStatsRequest{Pattern: "user>>>", Reset_: reset})
	if err != nil {
		return nil, fmt.Errorf("api xray: %w", err)
	}
	out := map[string]Traffic{}
	for _, s := range r.Stat {
		email, dir, ok := parseUserStat(s.Name)
		if !ok {
			continue
		}
		t := out[email]
		if dir == "uplink" {
			t.Uplink += s.Value
		} else {
			t.Downlink += s.Value
		}
		out[email] = t
	}
	return out, nil
}

// parseUserStat разбирает имя счётчика "user>>>EMAIL>>>traffic>>>uplink".
func parseUserStat(name string) (email, dir string, ok bool) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" || parts[1] == "" {
		return "", "", false
	}
	if parts[3] != "uplink" && parts[3] != "downlink" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

// FlowVision — единственный flow этапа 1.
const FlowVision = "xtls-rprx-vision"

// AddVLESSUser добавляет пользователя VLESS (flow Vision) в инбаунд с
// тегом tag без перезапуска ядра. email — id пользователя панели: по нему
// StatsService отдаёт его трафик. Уровень 0 — тот, для которого в policy
// включена статистика по пользователям.
func (a *API) AddVLESSUser(ctx context.Context, tag, email, uuid string) error {
	op := serial.ToTypedMessage(&handlercmd.AddUserOperation{User: &protocol.User{
		Level:   0,
		Email:   email,
		Account: serial.ToTypedMessage(&vless.Account{Id: uuid, Flow: FlowVision}),
	}})
	_, err := a.handler.AlterInbound(ctx, &handlercmd.AlterInboundRequest{Tag: tag, Operation: op})
	if err != nil {
		return fmt.Errorf("api xray: добавить %s в %s: %w", email, tag, err)
	}
	return nil
}

// RemoveUser убирает пользователя из инбаунда: активные соединения
// обрываются, новые не принимаются.
func (a *API) RemoveUser(ctx context.Context, tag, email string) error {
	op := serial.ToTypedMessage(&handlercmd.RemoveUserOperation{Email: email})
	_, err := a.handler.AlterInbound(ctx, &handlercmd.AlterInboundRequest{Tag: tag, Operation: op})
	if err != nil {
		return fmt.Errorf("api xray: удалить %s из %s: %w", email, tag, err)
	}
	return nil
}

// InboundUsers — email'ы пользователей, которые сейчас в инбаунде.
func (a *API) InboundUsers(ctx context.Context, tag string) ([]string, error) {
	r, err := a.handler.GetInboundUsers(ctx, &handlercmd.GetInboundUserRequest{Tag: tag})
	if err != nil {
		return nil, fmt.Errorf("api xray: пользователи %s: %w", tag, err)
	}
	out := make([]string, 0, len(r.Users))
	for _, u := range r.Users {
		out = append(out, u.Email)
	}
	return out, nil
}

// IsUserExists — Xray ответил «пользователь уже есть»: при сверке
// состояния это не ошибка, нужный пользователь уже на месте.
func IsUserExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

// IsUserNotFound — Xray ответил «пользователя нет»: удалять нечего.
func IsUserNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}
