// Сгенерировано scripts/sync-vpn4eva-parser.sh — не править вручную.
// Источник: github.com/lyovkindanil/Vpn4Eva, коммит 7a5ab396a8731568ccf22b736898b8fcebca3b7a:
//   core/internal/profile/server.go

package profile

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Протоколы серверов.
const (
	ProtoVLESS     = "vless"
	ProtoWireGuard = "wireguard"
	ProtoAmneziaWG = "amneziawg"
	ProtoHysteria2 = "hysteria2"
)

// Server — разобранный сервер любого протокола. Заполнено ровно одно из
// полей VLESS, WireGuard, Hysteria2 (AmneziaWG — это WireGuard с Amnezia).
type Server struct {
	Protocol string
	Name     string
	Host     string
	Port     uint16

	VLESS     *VLESS
	WireGuard *WireGuard
	Hysteria2 *Hysteria2
}

// ParseServer разбирает сервер: ссылку vless://, wireguard://, hysteria2://,
// ключ AmneziaVPN vpn:// или текст конфигурации WireGuard/AmneziaWG
// ([Interface] и [Peer]).
func ParseServer(raw string) (*Server, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\uFEFF"))
	if raw == "" {
		return nil, fmt.Errorf("сервер не задан")
	}
	if isWireGuardConf(raw) {
		return parseWireGuardConf(raw)
	}
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok {
		return nil, fmt.Errorf("ожидалась ссылка vless://, wireguard://, hysteria2://, ключ vpn:// или файл конфигурации WireGuard")
	}
	switch strings.ToLower(scheme) {
	case "vless":
		v, err := ParseVLESS(raw)
		if err != nil {
			return nil, err
		}
		return &Server{Protocol: ProtoVLESS, Name: v.Name, Host: v.Server, Port: v.Port, VLESS: v}, nil
	case "wireguard", "wg":
		return parseWireGuardURL(raw)
	case "hysteria2", "hy2":
		return parseHysteria2(raw)
	case "vpn":
		return parseAmneziaKey(raw)
	}
	return nil, fmt.Errorf("протокол %s:// пока не поддерживается", scheme)
}

// IsServerLink — строка похожа на ссылку поддерживаемого протокола.
// Подписки отбирают по ней строки, которые стоит разбирать.
func IsServerLink(s string) bool {
	scheme, _, ok := strings.Cut(s, "://")
	if !ok {
		return false
	}
	switch strings.ToLower(scheme) {
	case "vless", "wireguard", "wg", "hysteria2", "hy2", "vpn":
		return true
	}
	return false
}

// Address — host:port сервера.
func (s *Server) Address() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(int(s.Port)))
}

// UDP — протокол работает поверх UDP: задержку до такого сервера не
// измерить установкой TCP-соединения.
func (s *Server) UDP() bool { return s.Protocol != ProtoVLESS }

// Security — шифрование транспорта для подписи в GUI: reality, tls, none
// или mlkem (VLESS Encryption) у VLESS; tls у Hysteria2; пусто у WireGuard.
func (s *Server) Security() string {
	switch {
	case s.VLESS != nil:
		if s.VLESS.Encryption != "" && s.VLESS.Security == "none" {
			return "mlkem" // VLESS Encryption без TLS
		}
		return s.VLESS.Security
	case s.Hysteria2 != nil:
		if s.Hysteria2.Obfs != "" {
			return "tls+" + s.Hysteria2.Obfs
		}
		return "tls"
	}
	return ""
}

// Network — транспорт: tcp, grpc, ws у VLESS; quic у Hysteria2; udp у WireGuard.
func (s *Server) Network() string {
	switch {
	case s.VLESS != nil:
		return s.VLESS.Network
	case s.Hysteria2 != nil:
		return "quic"
	}
	return "udp"
}

// Identity — сервер и учётная запись без названия и второстепенных
// параметров: по ней подписка узнаёт сервер после смены ссылки.
func (s *Server) Identity() string {
	switch {
	case s.VLESS != nil:
		return fmt.Sprintf("vless:%s@%s", s.VLESS.UUID, s.Address())
	case s.WireGuard != nil:
		return fmt.Sprintf("wg:%s@%s", s.WireGuard.PublicKey, s.Address())
	case s.Hysteria2 != nil:
		return fmt.Sprintf("hy2:%s@%s", s.Hysteria2.Password, s.Address())
	}
	return ""
}

// splitHostPort разбирает host:port и [v6]:port; порт обязателен.
func splitHostPort(hostport, proto string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, fmt.Errorf("%s: некорректный адрес сервера %q", proto, hostport)
	}
	if host == "" {
		return "", 0, fmt.Errorf("%s: в адресе нет сервера", proto)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("%s: некорректный порт %q", proto, portStr)
	}
	return host, uint16(port), nil
}
