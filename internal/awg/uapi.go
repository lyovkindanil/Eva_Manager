package awg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SocketDir — каталог UAPI-сокетов amneziawg-go: туда же смотрит штатная
// утилита `awg show`.
const SocketDir = "/var/run/amneziawg"

// SocketPath — UAPI-сокет интерфейса.
func SocketPath(dir, iface string) string {
	if dir == "" {
		dir = SocketDir
	}
	return filepath.Join(dir, iface+".sock")
}

// DeviceUAPI — настройки устройства для UAPI (протокол wg set, ключи в
// hex): ключ, порт, маскировка и полный список пиров вместо прежнего.
func (in Interface) DeviceUAPI(peers []Peer) (string, error) {
	key, err := hexKey(in.Settings.PrivateKey)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nlisten_port=%d\n", key, in.Port)
	o := in.Settings.Obf
	for _, kv := range []struct {
		k string
		v int
	}{{"jc", o.Jc}, {"jmin", o.Jmin}, {"jmax", o.Jmax}, {"s1", o.S1}, {"s2", o.S2}, {"s3", o.S3}, {"s4", o.S4}} {
		if kv.v != 0 {
			fmt.Fprintf(&b, "%s=%d\n", kv.k, kv.v)
		}
	}
	for _, kv := range [][2]string{{"h1", o.H1}, {"h2", o.H2}, {"h3", o.H3}, {"h4", o.H4}, {"i1", o.I1}} {
		if kv[1] != "" {
			fmt.Fprintf(&b, "%s=%s\n", kv[0], kv[1])
		}
	}
	b.WriteString("replace_peers=true\n")
	for _, p := range peers {
		s, err := p.uapi(false)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// uapi — пир для UAPI; remove — удалить.
func (p Peer) uapi(remove bool) (string, error) {
	pubB64, err := p.PublicKey()
	if err != nil {
		return "", fmt.Errorf("пир %s: %w", p.UserID, err)
	}
	pub, _ := hexKey(pubB64)
	if remove {
		return fmt.Sprintf("public_key=%s\nremove=true\n", pub), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "public_key=%s\n", pub)
	if p.PresharedKey != "" {
		psk, err := hexKey(p.PresharedKey)
		if err != nil {
			return "", fmt.Errorf("пир %s: общий ключ: %w", p.UserID, err)
		}
		fmt.Fprintf(&b, "preshared_key=%s\n", psk)
	}
	fmt.Fprintf(&b, "replace_allowed_ips=true\nallowed_ip=%s/32\n", p.Address)
	return b.String(), nil
}

// PeersUAPI — добавить или обновить пиры (без замены остальных).
func PeersUAPI(add []Peer) (string, error) {
	var b strings.Builder
	for _, p := range add {
		s, err := p.uapi(false)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// RemoveUAPI — удалить пиры.
func RemoveUAPI(peers []Peer) (string, error) {
	var b strings.Builder
	for _, p := range peers {
		s, err := p.uapi(true)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// PeerStat — счётчики пира из UAPI (с запуска устройства).
type PeerStat struct {
	PublicKey     string // base64
	RxBytes       int64  // от клиента — его исходящий трафик
	TxBytes       int64  // клиенту — его входящий
	LastHandshake time.Time
}

// Client — UAPI-клиент устройства amneziawg-go через unix-сокет.
type Client struct {
	Socket string
}

func (c Client) do(ctx context.Context, req string) ([]string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if _, err := conn.Write([]byte(req + "\n")); err != nil {
		return nil, err
	}
	var lines []string
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			return lines, nil
		}
		if v, ok := strings.CutPrefix(line, "errno="); ok {
			if v != "0" {
				return lines, fmt.Errorf("AmneziaWG: UAPI errno=%s", v)
			}
			continue
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("AmneziaWG: UAPI оборвал ответ")
}

// Set применяет настройки (текст протокола wg set без «set=1»).
func (c Client) Set(ctx context.Context, cfg string) error {
	_, err := c.do(ctx, "set=1\n"+strings.TrimRight(cfg, "\n")+"\n")
	return err
}

// Peers — счётчики всех пиров.
func (c Client) Peers(ctx context.Context) ([]PeerStat, error) {
	lines, err := c.do(ctx, "get=1\n")
	if err != nil {
		return nil, err
	}
	var out []PeerStat
	var hsSec int64
	for _, line := range lines {
		k, v, _ := strings.Cut(line, "=")
		if k == "public_key" {
			out = append(out, PeerStat{PublicKey: hexToB64(v)})
			hsSec = 0
			continue
		}
		if len(out) == 0 {
			continue // поля устройства
		}
		p := &out[len(out)-1]
		n, _ := strconv.ParseInt(v, 10, 64)
		switch k {
		case "rx_bytes":
			p.RxBytes = n
		case "tx_bytes":
			p.TxBytes = n
		case "last_handshake_time_sec":
			hsSec = n
		case "last_handshake_time_nsec":
			if hsSec != 0 || n != 0 {
				p.LastHandshake = time.Unix(hsSec, n)
			}
		}
	}
	return out, nil
}
