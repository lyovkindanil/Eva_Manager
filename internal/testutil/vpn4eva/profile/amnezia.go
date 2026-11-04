// Сгенерировано scripts/sync-vpn4eva-parser.sh — не править вручную.
// Источник: github.com/lyovkindanil/Vpn4Eva, коммит 7a5ab396a8731568ccf22b736898b8fcebca3b7a:
//   core/internal/profile/amnezia.go

package profile

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// parseAmneziaKey разбирает ключ AmneziaVPN vpn://… — base64url от сжатого
// qCompress JSON. Внутри у контейнера amnezia-awg или amnezia-wireguard
// лежит готовый файл конфигурации; его и разбираем. Название берём из
// описания сервера. Встречается и vpn:// с текстом файла конфигурации
// прямо в base64 — так ссылки выдают панели AmneziaWG 3.x.
func parseAmneziaKey(raw string) (*Server, error) {
	const proto = "amnezia"
	data := strings.TrimSpace(raw[len("vpn://"):])
	var packed []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if packed, err = enc.DecodeString(data); err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s: ключ vpn:// повреждён", proto)
	}
	if text := string(packed); isWireGuardConf(text) {
		s, err := parseWireGuardConf(text)
		if err != nil {
			return nil, err
		}
		s.Name = confComment(text)
		return s, nil
	}
	js, err := qUncompress(packed)
	if err != nil {
		return nil, fmt.Errorf("%s: ключ vpn:// повреждён: %w", proto, err)
	}
	var key struct {
		Description      string `json:"description"`
		DefaultContainer string `json:"defaultContainer"`
		Containers       []struct {
			Container string        `json:"container"`
			AWG       *amneziaProto `json:"awg"`
			WireGuard *amneziaProto `json:"wireguard"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(js, &key); err != nil {
		return nil, fmt.Errorf("%s: ключ vpn:// повреждён: %w", proto, err)
	}
	var found []string
	for _, c := range key.Containers {
		var last string
		switch {
		case c.AWG != nil:
			last = c.AWG.LastConfig
		case c.WireGuard != nil:
			last = c.WireGuard.LastConfig
		}
		if last == "" {
			continue
		}
		var cfg struct {
			Config string `json:"config"`
		}
		if err := json.Unmarshal([]byte(last), &cfg); err != nil || !isWireGuardConf(cfg.Config) {
			continue
		}
		if c.Container == key.DefaultContainer {
			found = append([]string{cfg.Config}, found...)
		} else {
			found = append(found, cfg.Config)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%s: в ключе vpn:// нет конфигурации AmneziaWG или WireGuard — другие протоколы AmneziaVPN не поддерживаются", proto)
	}
	s, err := parseWireGuardConf(found[0])
	if err != nil {
		return nil, err
	}
	s.Name = strings.TrimSpace(key.Description)
	return s, nil
}

type amneziaProto struct {
	LastConfig string `json:"last_config"` // JSON с полем config — текстом файла
}

// qUncompress — формат qCompress из Qt: 4 байта длины (big-endian) и zlib.
// Встречается и zlib без длины.
func qUncompress(b []byte) ([]byte, error) {
	try := func(b []byte) ([]byte, error) {
		r, err := zlib.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(io.LimitReader(r, 1<<20))
	}
	if len(b) > 4 {
		if out, err := try(b[4:]); err == nil {
			return out, nil
		}
	}
	return try(b)
}

// confComment — текст первого комментария в файле конфигурации: панели
// пишут туда название клиента («# AmneziaWG_VpsLab1-1»).
func confComment(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			if c := strings.TrimSpace(strings.TrimLeft(line, "#")); c != "" {
				return c
			}
		}
	}
	return ""
}
