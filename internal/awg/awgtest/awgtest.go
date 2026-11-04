// Package awgtest — сервер и клиент AmneziaWG в памяти (amneziawg-go с
// netstack) для тестов панели: без root и настоящих интерфейсов.
package awgtest

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun/netstack"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	vpnparser "github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/profile"
)

// Server — устройство amneziawg-go с адресом сервера интерфейса и
// UAPI-сокетом, как у процесса awg-run. На :80 — сайт: /bytes?n=N
// отдаёт N байт.
func Server(t testing.TB, in awg.Interface) (socket string) {
	t.Helper()
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{in.ServerAddr().Addr()}, nil, in.Settings.MTU)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "awg-server: "))
	t.Cleanup(dev.Close)
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	socket = filepath.Join(t.TempDir(), in.Iface+".sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go dev.IpcHandle(c)
		}
	}()
	hl, err := tnet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hl.Close() })
	go http.Serve(hl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		io.WriteString(w, strings.Repeat("x", n))
	}))
	return socket
}

// FreeUDPPort — свободный UDP-порт на 127.0.0.1.
func FreeUDPPort(t testing.TB) int {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// Client — клиент amneziawg-go с настройками из конфигурации, которую
// панель выдаёт пользователю (разобранной парсером VPN4Eva). Возвращает
// HTTP-клиент, ходящий через туннель.
func Client(t testing.TB, conf string) *http.Client {
	t.Helper()
	s, err := vpnparser.ParseServer(conf)
	if err != nil {
		t.Fatal(err)
	}
	w := s.WireGuard
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{w.Address[0].Addr()}, nil, int(w.MTU))
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "awg-client: "))
	t.Cleanup(dev.Close)
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
	fmt.Fprintf(&b, "public_key=%s\n", h(w.PublicKey))
	if w.PresharedKey != "" {
		fmt.Fprintf(&b, "preshared_key=%s\n", h(w.PresharedKey))
	}
	fmt.Fprintf(&b, "endpoint=%s\nallowed_ip=0.0.0.0/0\n", s.Address())
	if err := dev.IpcSet(b.String()); err != nil {
		t.Fatal(err)
	}
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{DialContext: tnet.DialContext, DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}
