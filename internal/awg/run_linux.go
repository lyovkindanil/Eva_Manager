package awg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"github.com/vishvananda/netlink"
)

// RunOptions — параметры процесса AmneziaWG (команда eva-manager awg-run).
type RunOptions struct {
	Iface string
	// Addr — адрес сервера с маской подсети клиентов, например 10.8.0.1/24.
	Addr netip.Prefix
	MTU  int
	// SocketDir — каталог UAPI-сокета; пусто — SocketDir.
	SocketDir string
	// NAT — выпускать клиентов в интернет: ip_forward, MASQUERADE и
	// разрешения в FORWARD. Без него — только адреса самого сервера.
	NAT bool
	// AllowPrivate — пускать клиентов в частные сети за сервером
	// (локальная сеть, 169.254.169.254 облака). По умолчанию закрыто, как
	// у Xray.
	AllowPrivate bool
	Log          *slog.Logger
	// Ready вызывается, когда сокет UAPI принимает подключения.
	Ready func()
}

// Run поднимает интерфейс amneziawg-go и держит его до отмены ctx.
// Настройки (ключ, порт, маскировку, пиры) присылает панель через UAPI —
// процесс о базе ничего не знает и не хранит ключей на диске.
func Run(ctx context.Context, o RunOptions) error {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	sock := SocketPath(o.SocketDir, o.Iface)
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		return fmt.Errorf("каталог сокета: %w", err)
	}
	// Старый сокет от упавшего процесса.
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		return fmt.Errorf("интерфейс %s уже обслуживает другой процесс (%s)", o.Iface, sock)
	}
	os.Remove(sock)

	td, err := tun.CreateTUN(o.Iface, o.MTU)
	if err != nil {
		return fmt.Errorf("создать интерфейс %s: %w (нужны CAP_NET_ADMIN и /dev/net/tun)", o.Iface, err)
	}
	dlog := &device.Logger{
		Verbosef: func(string, ...any) {},
		Errorf: func(f string, a ...any) {
			msg := strings.TrimSpace(fmt.Sprintf(f, a...))
			// Ответ клиенту, который ещё не подключался или пропал: сервер
			// не знает, куда слать, — обычное дело, а не ошибка.
			if strings.Contains(msg, "no known endpoint for peer") {
				return
			}
			log.Warn(msg, "iface", o.Iface)
		},
	}
	dev := device.NewDevice(td, conn.NewDefaultBind(), dlog)
	defer dev.Close()

	link, err := netlink.LinkByName(o.Iface)
	if err != nil {
		return err
	}
	if err := netlink.AddrReplace(link, &netlink.Addr{IPNet: prefixNet(o.Addr)}); err != nil {
		return fmt.Errorf("адрес %s: %w", o.Addr, err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("поднять %s: %w", o.Iface, err)
	}
	if o.NAT {
		fw, err := setupForward(o, log)
		if err != nil {
			return err
		}
		defer fw.cleanup()
	}

	l, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("сокет UAPI: %w", err)
	}
	defer os.Remove(sock)
	defer l.Close()
	os.Chmod(sock, 0o600)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go dev.IpcHandle(c)
		}
	}()
	log.Info("AmneziaWG поднят", "iface", o.Iface, "addr", o.Addr, "socket", sock)
	if o.Ready != nil {
		o.Ready()
	}
	select {
	case <-ctx.Done():
		return nil
	case <-dev.Wait():
		return errors.New("устройство AmneziaWG остановилось")
	}
}

func prefixNet(p netip.Prefix) *net.IPNet {
	return &net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())}
}

// privateNets — куда клиентам нельзя без AllowPrivate.
var privateNets = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "fc00::/7", "fe80::/10"}

type forward struct {
	ipt   string
	rules [][]string // для удаления, в обратном порядке
	log   *slog.Logger
}

// setupForward включает ip_forward и добавляет правила iptables: NAT в
// интернет, разрешения в FORWARD (поверх политики DROP от Docker и ufw),
// запрет клиент↔клиент и, без AllowPrivate, в частные сети.
func setupForward(o RunOptions, log *slog.Logger) (*forward, error) {
	if err := enableForwarding(); err != nil {
		return nil, err
	}
	ipt, err := iptablesBin()
	if err != nil {
		return nil, err
	}
	f := &forward{ipt: ipt, log: log}
	subnet := o.Addr.Masked().String()
	// -I вставляет в начало: последняя вставленная — первая в цепочке.
	rules := [][]string{
		{"-t", "nat", "-I", "POSTROUTING", "-s", subnet, "!", "-o", o.Iface, "-j", "MASQUERADE"},
		{"-I", "FORWARD", "-o", o.Iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		{"-I", "FORWARD", "-i", o.Iface, "-j", "ACCEPT"},
	}
	if !o.AllowPrivate {
		for _, n := range privateNets {
			if strings.Contains(n, ":") {
				continue
			}
			rules = append(rules, []string{"-I", "FORWARD", "-i", o.Iface, "-d", n, "-j", "DROP"})
		}
	}
	rules = append(rules, []string{"-I", "FORWARD", "-i", o.Iface, "-o", o.Iface, "-j", "DROP"})
	for _, r := range rules {
		check := append([]string(nil), r...)
		for i, a := range check {
			if a == "-I" {
				check[i] = "-C"
			}
		}
		// Правило от прошлого запуска (процесс убили без уборки) — не дублируем.
		if exec.Command(ipt, check...).Run() == nil {
			f.rules = append(f.rules, r)
			continue
		}
		if out, err := exec.Command(ipt, r...).CombinedOutput(); err != nil {
			f.cleanup()
			return nil, fmt.Errorf("%s %s: %v: %s", filepath.Base(ipt), strings.Join(r, " "), err, strings.TrimSpace(string(out)))
		}
		f.rules = append(f.rules, r)
	}
	log.Info("NAT для клиентов AmneziaWG", "subnet", subnet, "iptables", filepath.Base(ipt))
	return f, nil
}

func (f *forward) cleanup() {
	for i := len(f.rules) - 1; i >= 0; i-- {
		r := append([]string(nil), f.rules[i]...)
		for j, a := range r {
			if a == "-I" {
				r[j] = "-D"
			}
		}
		if out, err := exec.Command(f.ipt, r...).CombinedOutput(); err != nil {
			f.log.Warn("правило iptables не удалено", "rule", strings.Join(r, " "), "err", strings.TrimSpace(string(out)))
		}
	}
	f.rules = nil
}

const ipForward = "/proc/sys/net/ipv4/ip_forward"

func enableForwarding() error {
	b, err := os.ReadFile(ipForward)
	if err == nil && strings.TrimSpace(string(b)) == "1" {
		return nil
	}
	if err := os.WriteFile(ipForward, []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("пересылка пакетов выключена и не включается (%v): выполните на сервере sysctl -w net.ipv4.ip_forward=1 и пропишите его в /etc/sysctl.conf", err)
	}
	return nil
}

// iptablesBin — iptables с тем же бэкендом, что у правил системы: если
// Docker или ufw живут в iptables-legacy, правила в nft на их политику
// FORWARD не влияют. Выбираем вариант, в котором уже больше правил.
func iptablesBin() (string, error) {
	count := func(bin string) int {
		p, err := exec.LookPath(bin)
		if err != nil {
			return -1
		}
		out, err := exec.Command(p).Output()
		if err != nil {
			return -1
		}
		return strings.Count(string(out), "\n-A ")
	}
	legacy, nft := count("iptables-legacy-save"), count("iptables-nft-save")
	switch {
	case legacy > nft && legacy > 0:
		if p, err := exec.LookPath("iptables-legacy"); err == nil {
			return p, nil
		}
	case nft >= 0 && nft >= legacy:
		if p, err := exec.LookPath("iptables-nft"); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath("iptables")
	if err != nil {
		return "", errors.New("iptables не найден: он нужен для выхода клиентов AmneziaWG в интернет (NAT)")
	}
	return p, nil
}
