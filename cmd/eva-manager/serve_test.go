package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// fakeXray пишет скрипт, который ведёт себя как xray (образец —
// internal/xray): version, run -test (отвергает конфиг со словом
// "broken"), run с "crash" падает сразу, с "diesoon" — через 2 секунды.
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
  if grep -q diesoon "$3"; then sleep 2; echo "fatal: inbound died"; exit 1; fi
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

// Apply: хороший конфиг применяется; конфиг, не прошедший проверку, и
// конфиг, с которым ядро падает, откатываются на рабочий — ядро не
// остаётся сломанным.
func TestSupervisorApply(t *testing.T) {
	bin := fakeXray(t)
	old := applyWatch
	applyWatch = 3 * time.Second
	t.Cleanup(func() { applyWatch = old })
	ctx := context.Background()
	cfgPath := filepath.Join(t.TempDir(), "run", "config.json")
	s := &supervisor{
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		proc: xray.NewProcess(bin, cfgPath),
		cfg:  []byte(`{"v":1}`),
	}
	if err := s.proc.Start(ctx, s.cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.proc.Stop() })

	// Хороший конфиг: ядро перезапущено с ним.
	pid := s.proc.State().PID
	if err := s.Apply(ctx, []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	if st := s.proc.State(); !st.Running || st.PID == pid {
		t.Errorf("после применения: %+v", st)
	}
	if string(s.cfg) != `{"v":2}` {
		t.Errorf("текущий конфиг: %s", s.cfg)
	}
	if data, _ := os.ReadFile(cfgPath); string(data) != `{"v":2}` {
		t.Errorf("конфиг на диске: %s", data)
	}

	// Конфиг не прошёл проверку: рабочее ядро не тронуто.
	pid = s.proc.State().PID
	err := s.Apply(ctx, []byte(`{"broken":1}`))
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("плохой конфиг: %v", err)
	}
	if st := s.proc.State(); !st.Running || st.PID != pid {
		t.Errorf("после отказа проверки процесс изменился: %+v", st)
	}
	if string(s.cfg) != `{"v":2}` {
		t.Errorf("текущий конфиг после отказа: %s", s.cfg)
	}

	// Ядро не запустилось с новым конфигом (занят порт) — откат.
	err = s.Apply(ctx, []byte(`{"crash":1}`))
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("падение при старте: %v", err)
	}
	if st := s.proc.State(); !st.Running {
		t.Errorf("после отката ядро не работает: %+v", st)
	}
	if string(s.cfg) != `{"v":2}` {
		t.Errorf("текущий конфиг после отката: %s", s.cfg)
	}
	if data, _ := os.ReadFile(cfgPath); string(data) != `{"v":2}` {
		t.Errorf("конфиг на диске после отката: %s", data)
	}

	// Ядро упало уже после запуска — тоже откат на рабочий конфиг.
	err = s.Apply(ctx, []byte(`{"diesoon":1}`))
	if err == nil || !strings.Contains(err.Error(), "упал с новым конфигом") {
		t.Fatalf("падение после старта: %v", err)
	}
	if st := s.proc.State(); !st.Running {
		t.Errorf("после второго отката ядро не работает: %+v", st)
	}
	if string(s.cfg) != `{"v":2}` {
		t.Errorf("текущий конфиг после второго отката: %s", s.cfg)
	}
	if !strings.Contains(s.problem(), "откачен") {
		t.Errorf("problem: %q", s.problem())
	}
}
