package awg

import (
	"net/netip"
	"strings"
	"testing"
)

// Порт и подсеть, занятые в системе (wg-easy держит 51820/udp, WireGuard —
// 10.8.0.0/24), панель обходит сама, а заданные вручную — отклоняет.
func TestNewOnNodeSkipsSystem(t *testing.T) {
	sys := System{
		UDPBusy: func(p int) bool { return p == 51820 },
		Nets:    []netip.Prefix{netip.MustParsePrefix("10.8.0.0/24"), netip.MustParsePrefix("10.9.0.0/16")},
	}
	in, err := NewOnNode("a", "n", Options{Host: "vpn.example.com", System: sys}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in.Port != 51821 || in.Settings.Subnet != "10.10.0.0/24" {
		t.Errorf("порт %d, подсеть %s; ожидались 51821 и 10.10.0.0/24", in.Port, in.Settings.Subnet)
	}
	second, err := NewOnNode("b", "n", Options{Host: "vpn.example.com", System: sys}, []Interface{in})
	if err != nil {
		t.Fatal(err)
	}
	if second.Port != 51822 || second.Settings.Subnet != "10.11.0.0/24" {
		t.Errorf("второй: порт %d, подсеть %s", second.Port, second.Settings.Subnet)
	}
	if _, err := NewOnNode("c", "n", Options{Host: "vpn.example.com", Port: 51820, System: sys}, nil); err == nil || !strings.Contains(err.Error(), "занят") {
		t.Errorf("занятый порт вручную: %v", err)
	}
	if _, err := NewOnNode("c", "n", Options{Host: "vpn.example.com", Subnet: "10.9.5.0/24", System: sys}, nil); err == nil || !strings.Contains(err.Error(), "пересекается") {
		t.Errorf("занятая подсеть вручную: %v", err)
	}
	// Без проверки системы — прежние значения по умолчанию.
	plain, err := NewOnNode("d", "n", Options{Host: "vpn.example.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Port != DefaultPort || plain.Settings.Subnet != DefaultSubnet {
		t.Errorf("без System: %d %s", plain.Port, plain.Settings.Subnet)
	}
}
