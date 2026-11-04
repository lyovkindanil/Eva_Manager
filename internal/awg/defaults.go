package awg

import (
	"fmt"
	"net/netip"
)

// NextIface — свободное имя awg0, awg1… среди интерфейсов узла.
func NextIface(existing []Interface) string {
	used := map[string]bool{}
	for _, in := range existing {
		used[in.Iface] = true
	}
	for i := 0; ; i++ {
		if n := fmt.Sprintf("awg%d", i); !used[n] {
			return n
		}
	}
}

// System — что занято в системе вне панели: порты других программ
// (wg-easy в Docker, WireGuard) и подсети интерфейсов и маршрутов. Пустые
// поля — не проверять.
type System struct {
	UDPBusy func(port int) bool
	Nets    []netip.Prefix
}

func (s System) portBusy(p int) bool { return s.UDPBusy != nil && s.UDPBusy(p) }

func (s System) netTaken(subnet string) bool {
	p, err := netip.ParsePrefix(subnet)
	if err != nil {
		return false
	}
	for _, n := range s.Nets {
		if n.Overlaps(p) {
			return true
		}
	}
	return false
}

// NextPort — свободный порт от DefaultPort: не занятый интерфейсами узла и
// другими программами.
func NextPort(existing []Interface, sys System) int {
	used := map[int]bool{}
	for _, in := range existing {
		used[in.Port] = true
	}
	p := DefaultPort
	for used[p] || sys.portBusy(p) {
		p++
	}
	return p
}

// NextSubnet — свободная подсеть 10.8.0.0/24, 10.9.0.0/24…, которая не
// пересекается с подсетями узла и системы (wg-easy и WireGuard тоже
// начинают с 10.8.0.0/24).
func NextSubnet(existing []Interface, sys System) string {
	var mine []netip.Prefix
	for _, in := range existing {
		if p, err := netip.ParsePrefix(in.Settings.Subnet); err == nil {
			mine = append(mine, p.Masked())
		}
	}
	for i := 8; i < 255; i++ {
		n := fmt.Sprintf("10.%d.0.0/24", i)
		p := netip.MustParsePrefix(n)
		taken := sys.netTaken(n)
		for _, m := range mine {
			taken = taken || m.Overlaps(p)
		}
		if !taken {
			return n
		}
	}
	return DefaultSubnet
}

// Options — что задаёт администратор при создании интерфейса; пустые
// поля заполняются свободными значениями узла.
type Options struct {
	Name, Iface, Host, Subnet, DNS string
	Port                           int
	// System — проверка занятого в системе: и для значений по
	// умолчанию, и для заданных администратором.
	System System
}

// NewOnNode — интерфейс с новыми ключами и маскировкой, свободными именем,
// портом и подсетью среди existing.
func NewOnNode(id, nodeID string, o Options, existing []Interface) (Interface, error) {
	if o.Iface == "" {
		o.Iface = NextIface(existing)
	}
	if o.Name == "" {
		o.Name = "AmneziaWG"
		if len(existing) > 0 {
			o.Name += " " + o.Iface
		}
	}
	if o.Port == 0 {
		o.Port = NextPort(existing, o.System)
	} else if o.System.portBusy(o.Port) {
		return Interface{}, fmt.Errorf("UDP-порт %d занят другой программой (например, wg-easy или WireGuard) — укажите другой или оставьте поле пустым", o.Port)
	}
	in, err := New(id, nodeID, o.Name, o.Iface, o.Port, o.Host)
	if err != nil {
		return Interface{}, err
	}
	if o.DNS != "" {
		in.Settings.DNS = o.DNS
	}
	in.Settings.Subnet = o.Subnet
	if in.Settings.Subnet == "" {
		in.Settings.Subnet = NextSubnet(existing, o.System)
	} else if o.System.netTaken(o.Subnet) {
		return Interface{}, fmt.Errorf("подсеть %s пересекается с сетью в системе (другой VPN или Docker) — укажите другую или оставьте поле пустым", o.Subnet)
	}
	return in, in.Validate()
}
