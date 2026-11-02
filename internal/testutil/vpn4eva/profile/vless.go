// Сгенерировано scripts/sync-vpn4eva-parser.sh — не править вручную.
// Источник: github.com/lyovkindanil/Vpn4Eva, коммит 7a5ab396a8731568ccf22b736898b8fcebca3b7a:
//   core/internal/profile/vless.go (импорт vlessenc переписан на локальную копию)

// Package profile описывает пользовательский профиль подключения:
// сервер (ссылка vless://, wireguard://, hysteria2:// или файл WireGuard)
// и правила раздельного туннелирования.
package profile

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/vlessenc"
)

// VLESS — разобранная ссылка vless:// (формат Xray/v2rayN).
type VLESS struct {
	Name        string
	UUID        string
	Server      string
	Port        uint16
	Flow        string // например xtls-rprx-vision
	Network     string // tcp | grpc | ws
	Security    string // reality | tls | none
	SNI         string
	ALPN        []string
	Fingerprint string // uTLS fingerprint: chrome, firefox, ...
	PublicKey   string // Reality: pbk
	ShortID     string // Reality: sid
	// MLKEM — Reality с гибридным ключом X25519MLKEM768 (нужен Xray-core
	// 26.9.8+; 3x-ui добавляет в ссылку support-x25519mlkem768=true).
	MLKEM bool
	// Encryption — VLESS Encryption из Xray-core (mlkem768x25519plus...);
	// пусто — без него (в ссылке encryption=none).
	Encryption string
	// Транспорт
	ServiceName string // grpc
	Path        string // ws
	Host        string // ws
}

// ParseVLESS разбирает ссылку вида
// vless://UUID@host:port?type=tcp&security=reality&pbk=...&sid=...&sni=...&fp=chrome&flow=xtls-rprx-vision#name
func ParseVLESS(raw string) (*VLESS, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("vless: некорректная ссылка: %w", err)
	}
	if u.Scheme != "vless" {
		return nil, fmt.Errorf("vless: ожидалась схема vless://, получено %q", u.Scheme)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("vless: в ссылке нет UUID")
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("vless: в ссылке нет адреса сервера")
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("vless: некорректный порт %q", u.Port())
	}
	q := u.Query()
	v := &VLESS{
		Name:        u.Fragment,
		UUID:        u.User.Username(),
		Server:      host,
		Port:        uint16(port),
		Flow:        q.Get("flow"),
		Network:     strings.ToLower(q.Get("type")),
		Security:    strings.ToLower(q.Get("security")),
		SNI:         q.Get("sni"),
		Fingerprint: q.Get("fp"),
		PublicKey:   q.Get("pbk"),
		ShortID:     q.Get("sid"),
		MLKEM:       isTrue(q.Get("support-x25519mlkem768")),
		ServiceName: q.Get("serviceName"),
		Path:        q.Get("path"),
		Host:        q.Get("host"),
	}
	if alpn := q.Get("alpn"); alpn != "" {
		v.ALPN = strings.Split(alpn, ",")
	}
	if e := q.Get("encryption"); e != "none" {
		v.Encryption = e
	}
	if v.Network == "" {
		v.Network = "tcp"
	}
	if v.Security == "" {
		v.Security = "none"
	}
	if err := v.validate(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *VLESS) validate() error {
	if _, err := vlessenc.ParseClient(v.Encryption); err != nil {
		return fmt.Errorf("vless: encryption: %w", err)
	}
	switch v.Network {
	case "tcp", "grpc", "ws":
	default:
		return fmt.Errorf("vless: транспорт %q пока не поддерживается (tcp, grpc, ws)", v.Network)
	}
	switch v.Security {
	case "reality":
		if v.PublicKey == "" {
			return fmt.Errorf("vless: для Reality нужен публичный ключ (pbk)")
		}
		if v.SNI == "" {
			return fmt.Errorf("vless: для Reality нужен sni")
		}
	case "tls", "none":
	default:
		return fmt.Errorf("vless: security=%q не поддерживается", v.Security)
	}
	return nil
}

func isTrue(s string) bool {
	b, err := strconv.ParseBool(s)
	return err == nil && b
}
