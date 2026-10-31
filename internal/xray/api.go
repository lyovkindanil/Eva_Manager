package xray

import (
	"context"
	"fmt"
	"strings"
	"time"

	statscmd "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// API — клиент gRPC API Xray. Соединение без TLS: API слушает только
// 127.0.0.1 (это проверяет config).
type API struct {
	conn  *grpc.ClientConn
	stats statscmd.StatsServiceClient
}

// Dial готовит клиент; само соединение устанавливается при первом вызове.
func Dial(addr string) (*API, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("api xray %s: %w", addr, err)
	}
	return &API{conn: conn, stats: statscmd.NewStatsServiceClient(conn)}, nil
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
