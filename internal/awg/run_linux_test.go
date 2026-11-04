package awg

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun/netstack"

	vpnparser "github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/profile"
)

// requireRoot пропускает тест без root: ему нужен настоящий интерфейс.
// В CI тесты AmneziaWG запускаются через sudo.
func requireRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("нужен root: тест создаёт настоящий интерфейс AmneziaWG")
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		t.Skip("нет /dev/net/tun")
	}
}

// Настоящий интерфейс: Run создаёт awgtest0 с адресом, сайт слушает адрес
// сервера в ядре, клиент (amneziawg-go в памяти) получает его через туннель.
// Правила NAT ставятся и снимаются.
func TestRun(t *testing.T) {
	requireRoot(t)
	in := newTestInterface(t)
	in.Iface = "awgtest0"
	in.Settings.Subnet = "10.77.0.0/24"
	in.Port = freeUDP(t)
	p := newTestPeer(t, in, "10.77.0.2")

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	dir := t.TempDir()
	go func() {
		done <- Run(ctx, RunOptions{Iface: in.Iface, Addr: in.ServerAddr(), MTU: in.Settings.MTU, SocketDir: dir, NAT: true,
			Ready: func() { close(ready) }})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("интерфейс не поднялся")
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}
	defer stop()

	cfg, err := in.DeviceUAPI([]Peer{p})
	if err != nil {
		t.Fatal(err)
	}
	if err := (Client{Socket: SocketPath(dir, in.Iface)}).Set(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	ipt, _ := iptablesBin()
	if out, _ := exec.Command(ipt, "-t", "nat", "-S", "POSTROUTING").Output(); !strings.Contains(string(out), "10.77.0.0/24") {
		t.Errorf("нет MASQUERADE:\n%s", out)
	}

	hl, err := net.Listen("tcp", net.JoinHostPort(in.ServerAddr().Addr().String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer hl.Close()
	go http.Serve(hl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "через awgtest0") }))

	in.LinkHost = "127.0.0.1"
	conf, _ := in.ClientConf(p, "")
	s, err := vpnparser.ParseServer(conf)
	if err != nil {
		t.Fatal(err)
	}
	ctun, cnet, err := netstack.CreateNetTUN([]netip.Addr{p.Address}, nil, in.Settings.MTU)
	if err != nil {
		t.Fatal(err)
	}
	cli := device.NewDevice(ctun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "client: "))
	defer cli.Close()
	if err := cli.IpcSet(clientUAPI(t, s)); err != nil {
		t.Fatal(err)
	}
	cli.Up()
	hc := http.Client{Transport: &http.Transport{DialContext: cnet.DialContext}, Timeout: 10 * time.Second}
	resp, err := hc.Get("http://" + hl.Addr().String() + "/")
	if err != nil {
		t.Fatal("через туннель:", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "через awgtest0" {
		t.Fatal(string(body))
	}

	stop()
	if _, err := net.InterfaceByName(in.Iface); err == nil {
		t.Error("интерфейс остался после остановки")
	}
	if out, _ := exec.Command(ipt, "-t", "nat", "-S", "POSTROUTING").Output(); strings.Contains(string(out), "10.77.0.0/24") {
		t.Errorf("MASQUERADE остался:\n%s", out)
	}
	if out, _ := exec.Command(ipt, "-S", "FORWARD").Output(); strings.Contains(string(out), in.Iface) {
		t.Errorf("FORWARD остался:\n%s", out)
	}
}
