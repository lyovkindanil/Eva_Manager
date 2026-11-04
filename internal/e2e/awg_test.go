package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/awg/awgtest"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/panel"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

// targetIP — «публичный» адрес сайта на сервере (TEST-NET-2): частные
// адреса VPN4Eva шлёт мимо туннеля, а этот уходит в AmneziaWG.
const targetIP = "198.51.100.7"

// awgNode — узел панели с AmneziaWG как в работе: база, panel.Panel и
// awg.Manager, который запускает настоящий `eva-manager awg-run` с
// настоящим интерфейсом. Нужен root (в CI — sudo). Сайт слушает targetIP
// на интерфейсе сервера.
type awgNode struct {
	st   *store.Store
	pn   *panel.Panel
	mgr  *awg.Manager
	in   awg.Interface
	user *store.User
}

func newAWGNode(t *testing.T) *awgNode {
	if os.Geteuid() != 0 {
		t.Skip("нужен root: тест поднимает настоящий интерфейс AmneziaWG")
	}
	ctx := context.Background()
	bin := filepath.Join(t.TempDir(), "eva-manager")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/eva-manager").CombinedOutput(); err != nil {
		t.Fatalf("сборка eva-manager: %v\n%s", err, out)
	}
	d, err := db.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "eva.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)
	node, err := st.EnsureLocalNode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, node.ID, "даша", "", 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Адрес сервера для клиента — не loopback: движок VPN4Eva привязывает
	// сокеты к внешнему интерфейсу, как в приложении.
	in, err := awg.New(store.NewID(), node.ID, "E2E AWG", "awge2e0", awgtest.FreeUDPPort(t), hostIP(t))
	if err != nil {
		t.Fatal(err)
	}
	in.Settings.Subnet = "10.66.0.0/24"
	if err := st.CreateAWG(ctx, in); err != nil {
		t.Fatal(err)
	}
	n := &awgNode{st: st, in: in, user: u}
	n.pn = &panel.Panel{Store: st, NodeID: node.ID}
	n.mgr = &awg.Manager{Bin: bin, SocketDir: t.TempDir(), OnUp: n.pn.PushAWG}
	n.pn.AWG = n.mgr
	t.Cleanup(n.mgr.Stop)
	if err := n.pn.SyncUsers(ctx); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, n.mgr, in.ID)

	// Сайт на «публичном» адресе сервера.
	link, err := netlink.LinkByName(in.Iface)
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.AddrAdd(link, &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP(targetIP), Mask: net.CIDRMask(32, 32)}}); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", targetIP+":80")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("eva-awg-ok ", 1000))
	}))
	return n
}

func hostIP(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	t.Skip("нет сетевого адаптера кроме loopback")
	return ""
}

func waitRunning(t *testing.T, m *awg.Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := m.Running(id); ok {
			time.Sleep(200 * time.Millisecond) // OnUp присылает настройки
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("интерфейс не поднялся: %+v", m.Statuses())
}

func (n *awgNode) access(t *testing.T) store.UserAWG {
	ua, err := n.st.UserAWGs(context.Background(), n.user.ID)
	if err != nil || len(ua) != 1 {
		t.Fatal(ua, err)
	}
	return ua[0]
}

func (n *awgNode) traffic(t *testing.T) int64 {
	if err := n.pn.CollectOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	u, err := n.st.UserByID(context.Background(), n.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u.TrafficUsed
}

// TestAWGServer — интерфейс поднимает настоящий процесс awg-run, панель
// присылает ему настройки, клиент (amneziawg-go по конфигурации панели)
// получает сайт через туннель, трафик учитывается. Падение процесса —
// перезапуск, и пиры возвращаются.
func TestAWGServer(t *testing.T) {
	n := newAWGNode(t)
	a := n.access(t)
	conf, err := a.Interface.ClientConf(a.Peer, "")
	if err != nil {
		t.Fatal(err)
	}
	get := func() {
		t.Helper()
		hc := awgtest.Client(t, conf)
		resp, err := hc.Get("http://" + targetIP + "/")
		if err != nil {
			t.Fatal("через туннель:", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.HasPrefix(string(b), "eva-awg-ok") {
			t.Fatal(string(b))
		}
	}
	get()
	// Сверка пользователей (каждые 30 секунд) не трогает пиров без
	// изменений: счётчики и сессия на месте.
	sock, _, _ := n.mgr.Running(n.in.ID)
	before, err := (awg.Client{Socket: sock}).Peers(context.Background())
	if err != nil || len(before) != 1 || before[0].RxBytes == 0 {
		t.Fatal(before, err)
	}
	if err := n.pn.SyncUsers(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := (awg.Client{Socket: sock}).Peers(context.Background())
	if len(after) != 1 || after[0].RxBytes < before[0].RxBytes || !after[0].LastHandshake.Equal(before[0].LastHandshake) {
		t.Fatalf("сверка сбросила пира: %+v → %+v", before, after)
	}
	if used := n.traffic(t); used < 11000 {
		t.Fatalf("трафик %d", used)
	}

	// Убитый процесс поднимается заново с теми же пирами.
	out, _ := exec.Command("pkill", "-f", "awg-run -iface "+n.in.Iface).CombinedOutput()
	time.Sleep(300 * time.Millisecond)
	waitRunning(t, n.mgr, n.in.ID)
	link, err := netlink.LinkByName(n.in.Iface)
	if err != nil {
		t.Fatalf("интерфейса нет после перезапуска: %v %s", err, out)
	}
	netlink.AddrAdd(link, &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP(targetIP), Mask: net.CIDRMask(32, 32)}})
	get()
}

// TestVPN4EvaAWG — ключ vpn:// панели открывает сам клиент VPN4Eva
// (vpn4eva-bench, тот же движок, что в приложении) и ходит через
// AmneziaWG панели.
func TestVPN4EvaAWG(t *testing.T) {
	bench := os.Getenv(EnvVPN4EvaBench)
	if bench == "" {
		t.Skipf("нужен %s", EnvVPN4EvaBench)
	}
	n := newAWGNode(t)
	a := n.access(t)
	key, err := a.Interface.VPNKey(a.Peer, "даша · E2E AWG")
	if err != nil {
		t.Fatal(err)
	}
	prof, _ := json.Marshal(map[string]string{"server": key})
	profPath := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(profPath, prof, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := []string{"-c", profPath, "-n", "5", "-url", "http://" + targetIP + "/", "-json"}
	if os.Getenv("EVA_TEST_BENCH_VERBOSE") != "" {
		args = append(args, "-v")
		os.WriteFile("/tmp/awg-profile.json", prof, 0o600)
	}
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, bench, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("vpn4eva-bench: %v\n%s\n%s", err, out, stderr.String())
	}
	defer func() {
		if t.Failed() {
			t.Log("журнал vpn4eva-bench:\n" + stderr.String())
		}
	}()
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
			t.Fatalf("ошибки через туннель: %v", r.Errors)
		}
	}
	if !found {
		t.Fatalf("нет серии через туннель:\n%s", out)
	}
	if used := n.traffic(t); used < 5*11000 {
		t.Fatalf("панель не видела трафика пользователя: %d", used)
	}
}
