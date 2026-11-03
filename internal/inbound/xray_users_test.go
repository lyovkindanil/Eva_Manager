package inbound

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// Пользователи добавляются и удаляются в работающем ядре через gRPC API,
// без перезапуска; сверка состояния опирается на InboundUsers.
// Нужен EVA_TEST_XRAY_BIN (в CI задан всегда).
func TestUsersViaRealXrayAPI(t *testing.T) {
	bin := os.Getenv(xray.EnvXrayBin)
	if bin == "" {
		t.Skipf("не задан %s", xray.EnvXrayBin)
	}
	ctx := context.Background()
	in := newTestInbound(t, false)
	in.Port = freePort(t)
	apiAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))

	cfg, err := BuildConfig(apiAddr, []Inbound{in})
	if err != nil {
		t.Fatal(err)
	}
	proc := xray.NewProcess(bin, filepath.Join(t.TempDir(), "config.json"))
	if err := proc.Start(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Stop() })

	api, err := xray.Dial(apiAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	waitFor(t, func() bool {
		_, err := api.SysStats(ctx)
		return err == nil
	})

	tag := in.Tag()
	if users, err := api.InboundUsers(ctx, tag); err != nil || len(users) != 0 {
		t.Fatalf("свежий инбаунд: %v, %v", users, err)
	}
	if err := api.AddVLESSUser(ctx, tag, "u1", testUUID); err != nil {
		t.Fatal(err)
	}
	if err := api.AddVLESSUser(ctx, tag, "u2", "0294b6e0-7a1d-7b8c-9d2e-3f4a5b6c7d8e"); err != nil {
		t.Fatal(err)
	}
	users, err := api.InboundUsers(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(users)
	if !slices.Equal(users, []string{"u1", "u2"}) {
		t.Fatalf("после добавления: %v", users)
	}
	// Дубль — «уже есть», сверка состояния считает это успехом.
	if err := api.AddVLESSUser(ctx, tag, "u1", testUUID); !xray.IsUserExists(err) {
		t.Errorf("дубль: %v", err)
	}
	if err := api.RemoveUser(ctx, tag, "u1"); err != nil {
		t.Fatal(err)
	}
	// Повторное удаление — «не найден».
	if err := api.RemoveUser(ctx, tag, "u1"); !xray.IsUserNotFound(err) {
		t.Errorf("повторное удаление: %v", err)
	}
	users, _ = api.InboundUsers(ctx, tag)
	if !slices.Equal(users, []string{"u2"}) {
		t.Errorf("после удаления: %v", users)
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

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("не дождались")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
