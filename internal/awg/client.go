package awg

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// obfLines — строки маскировки для .conf (имена как у awg-quick).
func (o Obfuscation) confLines() string {
	var b strings.Builder
	num := func(k string, v int) {
		if v != 0 {
			fmt.Fprintf(&b, "%s = %d\n", k, v)
		}
	}
	str := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s = %s\n", k, v)
		}
	}
	num("Jc", o.Jc)
	num("Jmin", o.Jmin)
	num("Jmax", o.Jmax)
	num("S1", o.S1)
	num("S2", o.S2)
	num("S3", o.S3)
	num("S4", o.S4)
	str("H1", o.H1)
	str("H2", o.H2)
	str("H3", o.H3)
	str("H4", o.H4)
	str("I1", o.I1)
	return b.String()
}

// Endpoint — адрес сервера для клиента, host:port.
func (in Interface) Endpoint() string {
	return net.JoinHostPort(in.LinkHost, strconv.Itoa(in.Port))
}

// ClientConf — файл конфигурации клиента (формат awg-quick): его
// принимают AmneziaVPN, AmneziaWG и VPN4Eva. label — подпись в первой
// строке-комментарии, её клиенты показывают как название.
func (in Interface) ClientConf(p Peer, label string) (string, error) {
	serverPub, err := PublicKey(in.Settings.PrivateKey)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if label != "" {
		fmt.Fprintf(&b, "# %s\n", strings.ReplaceAll(label, "\n", " "))
	}
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", p.PrivateKey)
	fmt.Fprintf(&b, "Address = %s/32\n", p.Address)
	if in.Settings.DNS != "" {
		fmt.Fprintf(&b, "DNS = %s\n", strings.Join(splitList(in.Settings.DNS), ", "))
	}
	fmt.Fprintf(&b, "MTU = %d\n", in.Settings.MTU)
	b.WriteString(in.Settings.Obf.confLines())
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", serverPub)
	if p.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", p.PresharedKey)
	}
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	fmt.Fprintf(&b, "Endpoint = %s\n", in.Endpoint())
	fmt.Fprintf(&b, "PersistentKeepalive = %d\n", DefaultKeepalive)
	return b.String(), nil
}

// VPNKey — ключ vpn:// в формате приложения AmneziaVPN (base64url от
// qCompress JSON с контейнером amnezia-awg). Его же понимает VPN4Eva.
func (in Interface) VPNKey(p Peer, label string) (string, error) {
	conf, err := in.ClientConf(p, label)
	if err != nil {
		return "", err
	}
	serverPub, _ := PublicKey(in.Settings.PrivateKey)
	o := in.Settings.Obf
	dns := splitList(in.Settings.DNS)
	dns = append(dns, "", "")
	port := strconv.Itoa(in.Port)
	last := map[string]any{
		"config":                conf,
		"hostName":              in.LinkHost,
		"port":                  in.Port,
		"client_ip":             p.Address.String(),
		"client_priv_key":       p.PrivateKey,
		"server_pub_key":        serverPub,
		"psk_key":               p.PresharedKey,
		"mtu":                   strconv.Itoa(in.Settings.MTU),
		"allowed_ips":           []string{"0.0.0.0/0", "::/0"},
		"persistent_keep_alive": strconv.Itoa(DefaultKeepalive),
		"Jc":                    strconv.Itoa(o.Jc), "Jmin": strconv.Itoa(o.Jmin), "Jmax": strconv.Itoa(o.Jmax),
		"S1": strconv.Itoa(o.S1), "S2": strconv.Itoa(o.S2), "S3": strconv.Itoa(o.S3), "S4": strconv.Itoa(o.S4),
		"H1": o.H1, "H2": o.H2, "H3": o.H3, "H4": o.H4,
	}
	if o.I1 != "" {
		last["I1"] = o.I1
	}
	lastJSON, err := json.Marshal(last)
	if err != nil {
		return "", err
	}
	key := map[string]any{
		"containers": []any{map[string]any{
			"container": "amnezia-awg",
			"awg": map[string]any{
				"port":            port,
				"transport_proto": "udp",
				"last_config":     string(lastJSON),
			},
		}},
		"defaultContainer": "amnezia-awg",
		"description":      label,
		"hostName":         in.LinkHost,
		"dns1":             dns[0],
		"dns2":             dns[1],
	}
	js, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	return "vpn://" + base64.RawURLEncoding.EncodeToString(qCompress(js)), nil
}

// qCompress — формат qCompress из Qt: длина (4 байта, big-endian) и zlib.
func qCompress(b []byte) []byte {
	var out bytes.Buffer
	binary.Write(&out, binary.BigEndian, uint32(len(b)))
	w, _ := zlib.NewWriterLevel(&out, zlib.BestCompression)
	w.Write(b)
	w.Close()
	return out.Bytes()
}
