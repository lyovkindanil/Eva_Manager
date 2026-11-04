package awg

import "fmt"

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

// NextPort — свободный порт от DefaultPort.
func NextPort(existing []Interface) int {
	used := map[int]bool{}
	for _, in := range existing {
		used[in.Port] = true
	}
	p := DefaultPort
	for used[p] {
		p++
	}
	return p
}

// NextSubnet — свободная подсеть 10.8.0.0/24, 10.9.0.0/24…
func NextSubnet(existing []Interface) string {
	used := map[string]bool{}
	for _, in := range existing {
		used[in.Settings.Subnet] = true
	}
	for i := 8; i < 255; i++ {
		if n := fmt.Sprintf("10.%d.0.0/24", i); !used[n] {
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
		o.Port = NextPort(existing)
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
		in.Settings.Subnet = NextSubnet(existing)
	}
	return in, in.Validate()
}
