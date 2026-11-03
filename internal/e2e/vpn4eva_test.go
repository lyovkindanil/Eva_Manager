package e2e

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	xraypanel "github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// EnvVPN4EvaBench — путь к vpn4eva-bench из репозитория клиента
// (core/cmd/vpn4eva-bench). Это настоящий движок VPN4Eva: тот же разбор
// ссылки, тот же пропатченный sing-box, что в приложении, только вместо
// TUN — локальный SOCKS. Репозиторий клиента приватный, поэтому в CI
// панели теста нет; собрать локально:
//
//	cd vpn4eva/core
//	export GOWORK="$(GOWORK=off go run ./tools/singbox-patch)"
//	go build -tags "$(cat ../build/TAGS)" -o /tmp/vpn4eva-bench ./cmd/vpn4eva-bench
const EnvVPN4EvaBench = "EVA_TEST_VPN4EVA_BENCH"

// TestVPN4EvaClient — ссылку панели открывает сам клиент VPN4Eva и
// прогоняет через Xray панели HTTP-запросы. Проверяет то, чего не видит
// TestLinkToConnection (там клиент — второй Xray): рукопожатие Reality
// с X25519MLKEM768 из пропатченного sing-box и разбор ссылки целиком, с
// настройками по умолчанию.
func TestVPN4EvaClient(t *testing.T) {
	xrayBin := os.Getenv(xraypanel.EnvXrayBin)
	bench := os.Getenv(EnvVPN4EvaBench)
	if xrayBin == "" || bench == "" {
		t.Skipf("нужны %s и %s", xraypanel.EnvXrayBin, EnvVPN4EvaBench)
	}
	t.Run("reality", func(t *testing.T) { vpn4evaClient(t, xrayBin, bench, false) })
	t.Run("reality+encryption", func(t *testing.T) { vpn4evaClient(t, xrayBin, bench, true) })
}

func vpn4evaClient(t *testing.T, xrayBin, bench string, encryption bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dest, destAddr := fakeDest(t, "e2e.test")
	defer dest.Close()
	xrayPort := freePort(t)
	in, err := inbound.NewVLESSReality(store.NewID(), "node", "E2E", xrayPort, "127.0.0.1", destAddr)
	if err != nil {
		t.Fatal(err)
	}
	in.Reality.ServerNames = []string{"e2e.test"}
	if encryption {
		if err := in.EnableEncryption("native", "0rtt", 600, ""); err != nil {
			t.Fatal(err)
		}
	}
	apiAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg, err := inbound.BuildConfig(apiAddr, []inbound.Inbound{in})
	if err != nil {
		t.Fatal(err)
	}
	cfg = allowPrivate(t, cfg)
	proc := xraypanel.NewProcess(xrayBin, filepath.Join(t.TempDir(), "xray", "config.json"))
	if err := proc.Start(ctx, cfg); err != nil {
		t.Fatalf("xray: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop() })
	api, err := xraypanel.Dial(apiAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	userID, uuid := store.NewID(), store.NewUUID()
	if err := api.AddVLESSUser(ctx, in.Tag(), userID, uuid); err != nil {
		t.Fatalf("пользователь в ядро: %v", err)
	}
	link, err := in.Link(uuid, "тест · E2E")
	if err != nil {
		t.Fatal(err)
	}

	// Цель — по имени: VPN4Eva шлёт частные IP мимо туннеля, а имя
	// (socks5h) уходит на сервер и резолвится там.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("eva-e2e-ok"))
	}))
	defer target.Close()
	targetURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)

	prof, _ := json.Marshal(map[string]string{"server": link})
	profPath := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(profPath, prof, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(ctx, bench, "-c", profPath, "-n", "5", "-url", targetURL, "-json").Output()
	if err != nil {
		t.Fatalf("vpn4eva-bench: %v\n%s\nxray:\n%s", err, out, proc.Logs())
	}
	var res struct {
		Results []struct {
			Name   string
			Stats  struct{ N, Failed int }
			Errors map[string]int
		}
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("вывод vpn4eva-bench: %v\n%s", err, out)
	}
	found := false
	for _, r := range res.Results {
		if !strings.HasPrefix(r.Name, "HTTP через") {
			continue
		}
		found = true
		t.Logf("%s: %d из %d", r.Name, r.Stats.N-r.Stats.Failed, r.Stats.N)
		if r.Stats.N == 0 || r.Stats.Failed != 0 {
			t.Fatalf("ошибки через туннель: %v\nxray:\n%s", r.Errors, proc.Logs())
		}
	}
	if !found {
		t.Fatalf("нет серии через туннель:\n%s", out)
	}
	// Трафик в ядре под email = id — запросы шли через сервер панели.
	traffic, err := api.UserTraffic(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if tt := traffic[userID]; tt.Downlink == 0 {
		t.Fatalf("ядро не видело трафика пользователя: %+v", traffic)
	}
}
