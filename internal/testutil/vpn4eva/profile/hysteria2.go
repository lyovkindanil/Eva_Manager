// Сгенерировано scripts/sync-vpn4eva-parser.sh — не править вручную.
// Источник: github.com/lyovkindanil/Vpn4Eva, коммит 7a5ab396a8731568ccf22b736898b8fcebca3b7a:
//   core/internal/profile/hysteria2.go

package profile

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Hysteria2 — разобранная ссылка hysteria2:// (формат официального клиента).
type Hysteria2 struct {
	Password string
	// Ports — диапазоны для прыжков по портам в формате sing-box "20000:30000";
	// пусто — только основной порт.
	Ports    []string
	SNI      string
	Insecure bool
	ALPN     []string
	// Obfs — маскировка: salamander; пусто — без неё.
	Obfs         string
	ObfsPassword string
}

// parseHysteria2 разбирает ссылку
// hysteria2://auth@host:443,20000-30000/?sni=…&insecure=1&obfs=salamander&obfs-password=…#name
// Схема hy2:// — то же самое. Порт по умолчанию 443.
func parseHysteria2(raw string) (*Server, error) {
	const proto = "hysteria2"
	// Адрес с прыжками по портам («host:443,20000-30000») net/url не
	// разбирает, поэтому отделяем его до разбора остального.
	_, rest, _ := strings.Cut(raw, "://")
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority := rest[:end]
	u, err := url.Parse("hysteria2://placeholder" + rest[end:])
	if err != nil {
		return nil, fmt.Errorf("%s: некорректная ссылка: %w", proto, err)
	}
	h := &Hysteria2{}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		user := authority[:at]
		authority = authority[at+1:]
		// Пароль в форме user:pass (userpass-авторизация) передаётся целиком.
		if h.Password, err = url.PathUnescape(user); err != nil {
			return nil, fmt.Errorf("%s: некорректный пароль в ссылке", proto)
		}
	}
	host, portSpec := authority, ""
	if strings.HasPrefix(host, "[") {
		i := strings.Index(host, "]")
		if i < 0 {
			return nil, fmt.Errorf("%s: некорректный адрес %q", proto, authority)
		}
		host, portSpec = host[1:i], strings.TrimPrefix(host[i+1:], ":")
	} else if i := strings.LastIndex(host, ":"); i >= 0 {
		host, portSpec = host[:i], host[i+1:]
	}
	if host == "" {
		return nil, fmt.Errorf("%s: в ссылке нет адреса сервера", proto)
	}
	q := u.Query()
	if mport := q.Get("mport"); mport != "" {
		if portSpec == "" {
			portSpec = mport
		} else {
			portSpec += "," + mport
		}
	}
	port := uint16(443)
	if portSpec != "" {
		port, h.Ports, err = parsePortSpec(portSpec)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", proto, err)
		}
	}
	if h.Password == "" {
		h.Password = q.Get("auth")
	}
	h.SNI = q.Get("sni")
	if h.SNI == "" {
		h.SNI = q.Get("peer")
	}
	switch strings.ToLower(q.Get("insecure")) {
	case "1", "true":
		h.Insecure = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		h.ALPN = strings.Split(alpn, ",")
	}
	switch obfs := strings.ToLower(q.Get("obfs")); obfs {
	case "", "none", "plain":
	case "salamander":
		h.Obfs = obfs
		h.ObfsPassword = q.Get("obfs-password")
		if h.ObfsPassword == "" {
			return nil, fmt.Errorf("%s: для salamander нужен obfs-password", proto)
		}
	default:
		return nil, fmt.Errorf("%s: obfs=%q не поддерживается", proto, obfs)
	}
	if h.Password == "" {
		return nil, fmt.Errorf("%s: в ссылке нет пароля (auth)", proto)
	}
	return &Server{Protocol: ProtoHysteria2, Name: u.Fragment, Host: host, Port: port, Hysteria2: h}, nil
}

// parsePortSpec разбирает «443», «443,20000-30000» или «20000-30000».
// Первый порт — основной; если портов несколько, все они уходят в ranges
// для прыжков по портам.
func parsePortSpec(spec string) (first uint16, ranges []string, err error) {
	multi := strings.ContainsAny(spec, ",-")
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := parsePort(lo)
		if err != nil {
			return 0, nil, err
		}
		b := a
		if isRange {
			if b, err = parsePort(hi); err != nil {
				return 0, nil, err
			}
			if b < a {
				return 0, nil, fmt.Errorf("диапазон портов %q задом наперёд", part)
			}
		}
		if first == 0 {
			first = a
		}
		if multi {
			ranges = append(ranges, fmt.Sprintf("%d:%d", a, b))
		}
	}
	return first, ranges, nil
}

func parsePort(s string) (uint16, error) {
	p, err := strconv.ParseUint(strings.TrimSpace(s), 10, 16)
	if err != nil || p == 0 {
		return 0, fmt.Errorf("некорректный порт %q", s)
	}
	return uint16(p), nil
}
