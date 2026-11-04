package sysnet

import (
	"net"
	"net/netip"
	"testing"
)

func TestPortsBusy(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if p := l.Addr().(*net.TCPAddr).Port; !TCPBusy(p) {
		t.Errorf("TCP %d слушается, а TCPBusy = false", p)
	}
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p := c.LocalAddr().(*net.UDPAddr).Port
	if !UDPBusy(p) {
		t.Errorf("UDP %d занят, а UDPBusy = false", p)
	}
	c.Close()
	if UDPBusy(p) {
		t.Errorf("UDP %d освобождён, а UDPBusy = true", p)
	}
}

func TestNetworks(t *testing.T) {
	// Хотя бы сеть основного интерфейса видна (на машине без сети — пропуск).
	nets := Networks()
	if len(nets) == 0 {
		t.Skip("нет интерфейсов IPv4")
	}
	for _, n := range nets {
		if n.Bits() == 0 {
			t.Errorf("маршрут по умолчанию попал в занятые: %v", n)
		}
	}
	if !Overlaps(netip.MustParsePrefix("10.8.0.0/24"), []netip.Prefix{netip.MustParsePrefix("10.8.0.0/16")}) {
		t.Error("Overlaps")
	}
}
