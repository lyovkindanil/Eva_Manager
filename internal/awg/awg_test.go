package awg

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun/netstack"

	vpnparser "github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/profile"
)

func newTestInterface(t *testing.T) Interface {
	t.Helper()
	in, err := New("i1", "local", "NL AWG", "awg0", 0, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func newTestPeer(t *testing.T, in Interface, addr string) Peer {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return Peer{InterfaceID: in.ID, UserID: "u-" + addr, PrivateKey: k, PresharedKey: NewPSK(), Address: netip.MustParseAddr(addr)}
}

// Случайная маскировка всегда проходит проверку клиента.
func TestNewObfuscationValid(t *testing.T) {
	for range 500 {
		o := NewObfuscation()
		if err := o.Validate(); err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		conf := "[Interface]\nPrivateKey = " + base64.StdEncoding.EncodeToString(make([]byte, 32)) +
			"\nAddress = 10.8.0.2/32\n" + o.confLines() +
			"[Peer]\nPublicKey = " + base64.StdEncoding.EncodeToString(make([]byte, 32)) + "\nAllowedIPs = 0.0.0.0/0\nEndpoint = 1.2.3.4:5\n"
		if _, err := vpnparser.ParseServer(conf); err != nil {
			t.Fatalf("клиент отверг %+v: %v", o, err)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := newTestInterface(t)
	cases := map[string]func(*Interface){
		"порт":       func(in *Interface) { in.Port = 70000 },
		"интерфейс":  func(in *Interface) { in.Iface = "awg 0" },
		"хост":       func(in *Interface) { in.LinkHost = "" },
		"ключ":       func(in *Interface) { in.Settings.PrivateKey = "abc" },
		"подсеть":    func(in *Interface) { in.Settings.Subnet = "10.8.0.1/24" },
		"подсеть v6": func(in *Interface) { in.Settings.Subnet = "fd00::/64" },
		"MTU":        func(in *Interface) { in.Settings.MTU = 9000 },
		"DNS":        func(in *Interface) { in.Settings.DNS = "1.1.1.1, dns.google" },
		"Jmax":       func(in *Interface) { in.Settings.Obf.Jmax = in.Settings.Obf.Jmin - 1 },
		"S2":         func(in *Interface) { in.Settings.Obf.S2 = in.Settings.Obf.S1 + 56 },
		"H пересекаются": func(in *Interface) {
			in.Settings.Obf.H1, in.Settings.Obf.H2 = "100-200", "150"
		},
		"H без маскировки 1–4": func(in *Interface) { in.Settings.Obf.H1, in.Settings.Obf.H2 = "", "1" },
		"I1":                   func(in *Interface) { in.Settings.Obf.I1 = "0xdead" },
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range cases {
		in := ok
		mut(&in)
		if in.Validate() == nil {
			t.Errorf("%s: ошибка не найдена", name)
		}
	}
}

func TestFreeAddr(t *testing.T) {
	in := newTestInterface(t)
	in.Settings.Subnet = "10.9.0.0/30" // .1 — сервер, .2 — единственный клиент, .3 — широковещательный
	if got := in.ServerAddr().String(); got != "10.9.0.1/30" {
		t.Fatal(got)
	}
	a, ok := in.FreeAddr(nil)
	if !ok || a.String() != "10.9.0.2" {
		t.Fatal(a, ok)
	}
	if a, ok := in.FreeAddr(map[netip.Addr]bool{a: true}); ok {
		t.Fatal("подсеть должна кончиться, выдан", a)
	}
}

// Конфигурацию и ключ vpn:// разбирает парсер клиента VPN4Eva и получает
// ровно то, что у сервера.
func TestClientConfig(t *testing.T) {
	in := newTestInterface(t)
	in.Settings.Obf.I1 = "<b 0xc2000000010811fe><r 16>"
	p := newTestPeer(t, in, "10.8.0.5")
	conf, err := in.ClientConf(p, "Даша · NL AWG")
	if err != nil {
		t.Fatal(err)
	}
	key, err := in.VPNKey(p, "Даша · NL AWG")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "vpn://") {
		t.Fatal(key)
	}
	serverPub, _ := PublicKey(in.Settings.PrivateKey)
	for name, raw := range map[string]string{"conf": conf, "vpn://": key} {
		s, err := vpnparser.ParseServer(raw)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, conf)
		}
		w := s.WireGuard
		o := in.Settings.Obf
		// Название клиент берёт из описания ключа vpn://; у .conf — из имени файла.
		if name == "vpn://" && s.Name != "Даша · NL AWG" {
			t.Fatalf("название %q", s.Name)
		}
		if s.Protocol != vpnparser.ProtoAmneziaWG || s.Host != "203.0.113.7" || s.Port != DefaultPort ||
			w.PrivateKey != p.PrivateKey || w.PublicKey != serverPub || w.PresharedKey != p.PresharedKey ||
			len(w.Address) != 1 || w.Address[0].String() != "10.8.0.5/32" || w.MTU != DefaultMTU || w.Keepalive != DefaultKeepalive {
			t.Fatalf("%s: %+v %+v", name, s, w)
		}
		a := w.Amnezia
		if a.Jc != o.Jc || a.Jmin != o.Jmin || a.Jmax != o.Jmax || a.S1 != o.S1 || a.S2 != o.S2 || a.S3 != o.S3 || a.S4 != o.S4 ||
			a.H1 != o.H1 || a.H2 != o.H2 || a.H3 != o.H3 || a.H4 != o.H4 || a.I1 != o.I1 {
			t.Fatalf("%s: маскировка %+v, ожидалась %+v", name, a, o)
		}
	}
}

// Сервер с настройками DeviceUAPI и клиент с настройками из .conf (через
// парсер VPN4Eva) — оба amneziawg-go в памяти — обмениваются данными:
// маскировка сервера и клиента совпадает. Заодно проверяются UAPI-клиент
// и его счётчики.
func TestInterop(t *testing.T) {
	in := newTestInterface(t)
	in.Settings.Obf.I1 = "<b 0xc2000000010811fe><r 16>"
	p := newTestPeer(t, in, "10.8.0.2")
	other := newTestPeer(t, in, "10.8.0.3")

	// Сервер: адрес 10.8.0.1, сайт на :80 внутри стека.
	stun, snet, err := netstack.CreateNetTUN([]netip.Addr{in.ServerAddr().Addr()}, nil, in.Settings.MTU)
	if err != nil {
		t.Fatal(err)
	}
	srv := device.NewDevice(stun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "server: "))
	defer srv.Close()
	in.Port = freeUDP(t)
	cfg, err := in.DeviceUAPI([]Peer{other})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.IpcSet(cfg); err != nil {
		t.Fatalf("сервер отверг настройки: %v\n%s", err, cfg)
	}
	if err := srv.Up(); err != nil {
		t.Fatal(err)
	}
	// UAPI-сокет, как у awg-run.
	dir := t.TempDir()
	sock := filepath.Join(dir, "awg0.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go srv.IpcHandle(c)
		}
	}()
	uapi := Client{Socket: sock}
	ctx := context.Background()
	add, _ := PeersUAPI([]Peer{p})
	if err := uapi.Set(ctx, add); err != nil {
		t.Fatal(err)
	}
	rm, _ := RemoveUAPI([]Peer{other})
	if err := uapi.Set(ctx, rm); err != nil {
		t.Fatal(err)
	}

	hl, err := snet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer hl.Close()
	go http.Serve(hl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 100000))
	}))

	// Клиент — из .conf, разобранного VPN4Eva.
	in.LinkHost = "127.0.0.1"
	conf, _ := in.ClientConf(p, "")
	s, err := vpnparser.ParseServer(conf)
	if err != nil {
		t.Fatal(err)
	}
	ctun, cnet, err := netstack.CreateNetTUN([]netip.Addr{p.Address}, nil, int(s.WireGuard.MTU))
	if err != nil {
		t.Fatal(err)
	}
	cli := device.NewDevice(ctun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "client: "))
	defer cli.Close()
	if err := cli.IpcSet(clientUAPI(t, s)); err != nil {
		t.Fatal(err)
	}
	if err := cli.Up(); err != nil {
		t.Fatal(err)
	}
	hc := http.Client{Transport: &http.Transport{DialContext: cnet.DialContext}, Timeout: 10 * time.Second}
	resp, err := hc.Get("http://" + in.ServerAddr().Addr().String() + "/")
	if err != nil {
		t.Fatal("через туннель:", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(body) != 100000 {
		t.Fatal(len(body))
	}

	stats, err := uapi.Peers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := p.PublicKey()
	if len(stats) != 1 || stats[0].PublicKey != pub || stats[0].TxBytes < 100000 || stats[0].RxBytes == 0 || stats[0].LastHandshake.IsZero() {
		t.Fatalf("%+v", stats)
	}
}

// clientUAPI — настройки клиента amneziawg-go из разобранного .conf.
func clientUAPI(t *testing.T, s *vpnparser.Server) string {
	w := s.WireGuard
	h := func(b64 string) string {
		b, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b)
	}
	a := w.Amnezia
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", h(w.PrivateKey))
	fmt.Fprintf(&b, "jc=%d\njmin=%d\njmax=%d\ns1=%d\ns2=%d\ns3=%d\ns4=%d\nh1=%s\nh2=%s\nh3=%s\nh4=%s\n",
		a.Jc, a.Jmin, a.Jmax, a.S1, a.S2, a.S3, a.S4, a.H1, a.H2, a.H3, a.H4)
	if a.I1 != "" {
		fmt.Fprintf(&b, "i1=%s\n", a.I1)
	}
	fmt.Fprintf(&b, "public_key=%s\npreshared_key=%s\nendpoint=%s\nallowed_ip=0.0.0.0/0\n", h(w.PublicKey), h(w.PresharedKey), s.Address())
	return b.String()
}

func freeUDP(t *testing.T) int {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}
