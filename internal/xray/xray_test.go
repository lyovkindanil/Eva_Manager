package xray

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// EnvXrayBin — путь к настоящему Xray для интеграционных тестов (так делает CI).
const EnvXrayBin = "EVA_TEST_XRAY_BIN"

// fakeXray пишет скрипт, который ведёт себя как xray: version, run -test
// (отвергает конфиг со словом "broken") и run (печатает строку и ждёт).
func fakeXray(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("скрипт-заглушка только для unix")
	}
	path := filepath.Join(t.TempDir(), "xray")
	script := `#!/bin/sh
case "$1" in
version) echo "Xray 26.10.21 (Xray, Penetrates Everything.) 0123abc (go1.26 linux/amd64)"; exit 0;;
run)
  if [ "$2" = "-test" ]; then
    if grep -q broken "$4"; then echo "Failed to start: infra/conf: broken config"; exit 23; fi
    echo "Configuration OK."; exit 0
  fi
  if grep -q crash "$3"; then echo "listen tcp :443: bind: address already in use"; exit 1; fi
  echo "Xray started"
  trap 'exit 0' TERM
  while true; do sleep 0.1; done;;
esac
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBaseConfigShape(t *testing.T) {
	b, err := BaseConfig("127.0.0.1:10085")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		API struct {
			Listen   string   `json:"listen"`
			Services []string `json:"services"`
		} `json:"api"`
		Policy struct {
			Levels map[string]map[string]bool `json:"levels"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.API.Listen != "127.0.0.1:10085" || !strings.Contains(strings.Join(cfg.API.Services, ","), "StatsService") {
		t.Errorf("api: %+v", cfg.API)
	}
	if !cfg.Policy.Levels["0"]["statsUserUplink"] || !cfg.Policy.Levels["0"]["statsUserDownlink"] {
		t.Errorf("статистика пользователей выключена: %+v", cfg.Policy.Levels)
	}
	if _, err := BaseConfig("10085"); err == nil {
		t.Error("адрес без хоста принят")
	}
}

func TestFakeVersionAndTest(t *testing.T) {
	bin := fakeXray(t)
	ctx := context.Background()
	v, err := Version(ctx, bin)
	if err != nil || v != "26.10.21" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if err := Test(ctx, bin, []byte(`{"ok":true}`)); err != nil {
		t.Errorf("хороший конфиг: %v", err)
	}
	err = Test(ctx, bin, []byte(`{"broken":true}`))
	var te *TestError
	if !errors.As(err, &te) || !strings.Contains(te.Output, "broken config") {
		t.Errorf("плохой конфиг: %v", err)
	}
}

func TestFindBin(t *testing.T) {
	if _, err := FindBin(filepath.Join(t.TempDir(), "нет")); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("err = %v", err)
	}
	bin := fakeXray(t)
	if p, err := FindBin(bin); err != nil || p != bin {
		t.Errorf("FindBin = %q, %v", p, err)
	}
}

func TestProcessLifecycle(t *testing.T) {
	bin := fakeXray(t)
	old := startGrace
	startGrace = 200 * time.Millisecond
	t.Cleanup(func() { startGrace = old })
	ctx := context.Background()
	cfgPath := filepath.Join(t.TempDir(), "run", "config.json")
	p := NewProcess(bin, cfgPath)

	// Плохой конфиг не запускается и не записывается.
	if err := p.Start(ctx, []byte(`{"broken":1}`)); err == nil {
		t.Fatal("плохой конфиг запущен")
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("плохой конфиг записан на диск: %v", err)
	}

	if err := p.Start(ctx, []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	if s := p.State(); !s.Running || s.PID == 0 {
		t.Errorf("state: %+v", s)
	}
	if err := p.Start(ctx, []byte(`{"v":1}`)); !errors.Is(err, ErrRunning) {
		t.Errorf("второй запуск: %v", err)
	}
	if fi, err := os.Stat(cfgPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("конфиг: %v, %v", fi, err)
	}

	// Restart с плохим конфигом не трогает рабочий процесс.
	pid := p.State().PID
	if err := p.Restart(ctx, []byte(`{"broken":2}`)); err == nil {
		t.Error("restart с плохим конфигом прошёл")
	}
	if s := p.State(); !s.Running || s.PID != pid {
		t.Errorf("после отказа restart процесс изменился: %+v", s)
	}
	data, _ := os.ReadFile(cfgPath)
	if string(data) != `{"v":1}` {
		t.Errorf("рабочий конфиг перезаписан: %s", data)
	}

	if err := p.Restart(ctx, []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	if s := p.State(); !s.Running || s.PID == pid {
		t.Errorf("restart не перезапустил: %+v", s)
	}
	waitFor(t, func() bool { return len(p.Logs()) > 0 })
	if !strings.Contains(strings.Join(p.Logs(), "\n"), "Xray started") {
		t.Errorf("журнал: %q", p.Logs())
	}

	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if s := p.State(); s.Running || s.LastError != "" {
		t.Errorf("после Stop: %+v", s)
	}
}

func TestProcessCrashOnStart(t *testing.T) {
	bin := fakeXray(t)
	p := NewProcess(bin, filepath.Join(t.TempDir(), "config.json"))
	err := p.Start(context.Background(), []byte(`{"crash":1}`))
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("err = %v", err)
	}
	if s := p.State(); s.Running || s.LastError == "" {
		t.Errorf("state: %+v", s)
	}
}

func TestRing(t *testing.T) {
	r := newRing(3)
	for _, s := range []string{"a", "b"} {
		r.add(s)
	}
	if got := strings.Join(r.lines(), ""); got != "ab" {
		t.Errorf("%q", got)
	}
	for _, s := range []string{"c", "d", "e"} {
		r.add(s)
	}
	if got := strings.Join(r.lines(), ""); got != "cde" {
		t.Errorf("%q", got)
	}
	if got := strings.Join(r.tail(2), ""); got != "de" {
		t.Errorf("%q", got)
	}
}

// TestRealXray — базовый конфиг принимает настоящий Xray, процесс
// запускается и отвечает по gRPC API. Нужен EVA_TEST_XRAY_BIN.
func TestRealXray(t *testing.T) {
	bin := os.Getenv(EnvXrayBin)
	if bin == "" {
		t.Skipf("не задан %s", EnvXrayBin)
	}
	ctx := context.Background()
	v, err := Version(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Xray %s", v)

	addr := freePort(t)
	cfg, err := BaseConfig(addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := Test(ctx, bin, cfg); err != nil {
		t.Fatalf("базовый конфиг: %v", err)
	}
	var te *TestError
	if err := Test(ctx, bin, []byte(`{"inbounds":[{"protocol":"vless","port":"не порт"}]}`)); !errors.As(err, &te) {
		t.Errorf("плохой конфиг: %v", err)
	}

	p := NewProcess(bin, filepath.Join(t.TempDir(), "config.json"))
	if err := p.Start(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	api, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	var sys *SysStats
	waitFor(t, func() bool {
		sys, err = api.SysStats(ctx)
		return err == nil
	})
	if sys.Goroutines == 0 {
		t.Errorf("sys: %+v", sys)
	}
	if _, err := api.UserTraffic(ctx, false); err != nil {
		t.Errorf("UserTraffic: %v", err)
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("не дождались")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
