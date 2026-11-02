// Сгенерировано scripts/sync-vpn4eva-parser.sh — не править вручную.
// Источник: github.com/lyovkindanil/Vpn4Eva, коммит 7a5ab396a8731568ccf22b736898b8fcebca3b7a:
//   patches/sing-box/common/vlessenc/vpn4eva.go (только Config и ParseClient: полная реализация передачи данных панели не нужна)

package vlessenc

import (
	"encoding/base64"
	"errors"
	"strings"
)

// Config — разобранная строка encryption клиента:
// mlkem768x25519plus.<native|xorpub|random>.<1rtt|0rtt>[.<паддинг>...].<ключ>[.<ключ>...]
type Config struct {
	XorMode uint32
	Seconds uint32
	Padding string
	Keys    [][]byte // X25519 (32 байта) или ML-KEM-768 (1184 байта), по порядку
}

// ParseClient разбирает encryption; "" и "none" — без шифрования (nil, nil).
func ParseClient(encryption string) (*Config, error) {
	if encryption == "" || encryption == "none" {
		return nil, nil
	}
	s := strings.Split(encryption, ".")
	if len(s) < 4 || s[0] != "mlkem768x25519plus" {
		return nil, errors.New(`vless: unsupported encryption "` + encryption + `"`)
	}
	c := &Config{}
	switch s[1] {
	case "native":
	case "xorpub":
		c.XorMode = 1
	case "random":
		c.XorMode = 2
	default:
		return nil, errors.New("vless encryption: unknown mode " + s[1])
	}
	switch s[2] {
	case "1rtt":
	case "0rtt":
		c.Seconds = 1
	default:
		return nil, errors.New("vless encryption: unknown handshake " + s[2])
	}
	var padding []string
	for _, r := range s[3:] {
		// Как в Xray-core: короткие части — паддинг, длинные — ключи.
		if len(r) < 20 {
			if len(c.Keys) > 0 {
				return nil, errors.New("vless encryption: padding after keys")
			}
			padding = append(padding, r)
			continue
		}
		b, _ := base64.RawURLEncoding.DecodeString(r)
		if len(b) != 32 && len(b) != 1184 {
			return nil, errors.New("vless encryption: bad key " + r)
		}
		c.Keys = append(c.Keys, b)
	}
	if len(c.Keys) == 0 {
		return nil, errors.New("vless encryption: no keys")
	}
	c.Padding = strings.Join(padding, ".")
	return c, nil
}
