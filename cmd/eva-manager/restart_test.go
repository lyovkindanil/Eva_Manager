package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/panel"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// После падения ядра supervisor поднимает его с конфигом без
// пользователей; пользователи должны вернуться сразу, а не через тик
// сборщика (30 секунд). То же после применения конфига.
func TestUsersBackAfterRestart(t *testing.T) {
	bin := os.Getenv(xray.EnvXrayBin)
	if bin == "" {
		t.Skipf("нужен %s", xray.EnvXrayBin)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.New(dbtest.SQLite(t))
	node, err := st.EnsureLocalNode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in, err := inbound.NewVLESSReality(store.NewID(), node.ID, "NL-1", freePort(t), "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateInbound(ctx, in); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, node.ID, "danil", "", 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	apiAddr := "127.0.0.1:" + strconv.Itoa(freePort(t))
	cfg, err := inbound.BuildConfig(apiAddr, []inbound.Inbound{in})
	if err != nil {
		t.Fatal(err)
	}
	api, err := xray.Dial(apiAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &supervisor{log: log, proc: xray.NewProcess(bin, filepath.Join(t.TempDir(), "config.json")), cfg: cfg}
	pn := &panel.Panel{Store: st, API: api, Core: s, APIAddr: apiAddr, NodeID: node.ID, Log: log}
	s.onStart = func(ctx context.Context) { _ = pn.SyncUsers(ctx) }
	s.beforeStop = func(ctx context.Context) { _ = pn.CollectOnce(ctx) }
	s.wg.Add(1)
	go s.run(ctx)
	defer func() { cancel(); s.stop() }() // как в serve: сначала отмена, потом остановка

	hasUser := func() bool {
		users, err := api.InboundUsers(ctx, in.Tag())
		return err == nil && slices.Contains(users, u.ID)
	}
	waitFor(t, "пользователь после первого запуска", hasUser)

	pid := s.proc.State().PID
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ядро перезапущено", func() bool { st := s.proc.State(); return st.Running && st.PID != pid })
	waitFor(t, "пользователь после падения ядра", hasUser)

	old := applyWatch
	applyWatch = 500 * time.Millisecond
	t.Cleanup(func() { applyWatch = old })
	if err := s.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if !hasUser() {
		t.Error("пользователя нет сразу после применения")
	}
	if p := s.problem(); p != "" {
		t.Errorf("после применения висит проблема: %q", p)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("не дождались: %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
