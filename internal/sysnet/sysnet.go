// Package sysnet проверяет, что занято в системе чужими программами:
// порты (другая панель, nginx, wg-easy в Docker) и подсети (WireGuard,
// Docker). Панель сверяется с ним, прежде чем дать порт или подсеть новому
// подключению.
package sysnet

import (
	"bufio"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// TCPBusy — кто-то слушает TCP-порт.
func TCPBusy(port int) bool { return busy("tcp", port) }

// UDPBusy — UDP-порт занят.
func UDPBusy(port int) bool { return busy("udp", port) }

func busy(proto string, port int) bool {
	if ports, ok := procPorts(proto); ok {
		return ports[port]
	}
	// Нет /proc (не Linux): пробуем занять порт сами. Нет прав на порт
	// ниже 1024 — не знаем, считаем свободным.
	var err error
	if proto == "tcp" {
		var l net.Listener
		if l, err = net.Listen("tcp", ":"+strconv.Itoa(port)); err == nil {
			l.Close()
		}
	} else {
		var c net.PacketConn
		if c, err = net.ListenPacket("udp", ":"+strconv.Itoa(port)); err == nil {
			c.Close()
		}
	}
	return errors.Is(err, syscall.EADDRINUSE)
}

// procPorts — локальные порты из /proc/net/{tcp,tcp6,udp,udp6}: для TCP
// только слушающие сокеты (состояние 0A), для UDP — все.
func procPorts(proto string) (map[int]bool, bool) {
	ports := map[int]bool{}
	found := false
	for _, f := range []string{proto, proto + "6"} {
		file, err := os.Open("/proc/net/" + f)
		if err != nil {
			continue
		}
		found = true
		sc := bufio.NewScanner(file)
		sc.Scan() // заголовок
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 4 {
				continue
			}
			if proto == "tcp" && fields[3] != "0A" {
				continue
			}
			_, p, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			if n, err := strconv.ParseUint(p, 16, 16); err == nil {
				ports[int(n)] = true
			}
		}
		file.Close()
	}
	return ports, found
}

// Networks — подсети IPv4, занятые в системе: адреса интерфейсов и
// маршруты (WireGuard, Docker, VPN), кроме маршрута по умолчанию.
func Networks() []netip.Prefix {
	var out []netip.Prefix
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() && !p.Addr().IsLoopback() {
				out = append(out, p.Masked())
			}
		}
	}
	if f, err := os.Open("/proc/net/route"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Scan()
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 8 {
				continue
			}
			dst, err1 := hexIP(fields[1])
			mask, err2 := hexIP(fields[7])
			if err1 != nil || err2 != nil {
				continue
			}
			bits := 0
			for _, b := range mask.As4() {
				for ; b != 0; b <<= 1 {
					bits++
				}
			}
			if bits == 0 {
				continue
			}
			out = append(out, netip.PrefixFrom(dst, bits).Masked())
		}
	}
	return out
}

// hexIP — адрес из /proc/net/route (little-endian hex).
func hexIP(s string) (netip.Addr, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return netip.Addr{}, errors.New("bad address")
	}
	return netip.AddrFrom4([4]byte{b[3], b[2], b[1], b[0]}), nil
}

// Overlaps — подсеть p пересекается с одной из nets.
func Overlaps(p netip.Prefix, nets []netip.Prefix) bool {
	for _, n := range nets {
		if n.Overlaps(p) {
			return true
		}
	}
	return false
}
